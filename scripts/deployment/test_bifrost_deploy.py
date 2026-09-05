"""Offline tests for bifrost-deploy: no remote DB, paid API, or real /opt writes."""
import copy
import importlib.machinery
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch


loader = importlib.machinery.SourceFileLoader('bifrost_deploy', str(Path(__file__).with_name('bifrost-deploy')))
spec = importlib.util.spec_from_loader(loader.name, loader)
bf = importlib.util.module_from_spec(spec)
loader.exec_module(bf)


def config(role):
    env = 'test' if role == 'test' else 'prod'
    return {key: {'enabled': True, 'type': 'postgres', 'config': {
        'host': '10.1.12.7', 'port': '5432', 'user': f'bf_{env}',
        'password': 'env.BF_DB_PASSWORD', 'db_name': f'bifrost_{env}_{suffix}', 'ssl_mode': 'require',
    }} for key, suffix in [('config_store', 'config'), ('logs_store', 'logs')]}


def service(role):
    return {'image': 'bifrost-moon:2.0.0-moon.18', 'container_name': 'bifrost',
            'ports': [{'target': 8080, 'published': '18080' if role == 'test' else '8080', 'protocol': 'tcp'}],
            'environment': {'GOGC': '100'}, 'restart': 'unless-stopped', 'volumes': [],
            'mem_limit': '6g', 'extra_hosts': {'langfuse-archive.tailb34b09.ts.net': '100.108.96.112'}}


class FakeDocker:
    def __init__(self, root, legacy):
        self.root = root
        self.calls = []
        self.fail_start = False
        self.exit_code = 0
        self.next_id = 1
        self.images = {f'bifrost-moon:2.0.0-moon.{n}': {
            'Id': 'sha256:' + str(n) * 32, 'Os': 'linux', 'Architecture': 'amd64',
            'Config': {'User': '1000:0'}, 'RootFS': {'Layers': [f'layer{n}']}, 'Created': '2026-09-03',
        } for n in [18, 19]}
        self.items = {'old': {'Id': 'old', 'Name': '/bifrost', 'Image': self.images['bifrost-moon:2.0.0-moon.18']['Id'],
            'Config': {'Image': 'bifrost-moon:2.0.0-moon.18', 'Labels': {
                'com.docker.compose.project': 'bifrost-node17',
                'com.docker.compose.project.working_dir': str(legacy), 'bifrost.deployment_role': 'test'}},
            'HostConfig': {'RestartPolicy': {'Name': 'unless-stopped'}, 'PortBindings': {}},
            'State': {'Running': True, 'Status': 'running', 'StartedAt': '2026-09-03T01:00:00Z',
                      'ExitCode': 0, 'OOMKilled': False, 'Health': {'Status': 'healthy'}}, 'Mounts': []}}

    def named(self):
        return next((v for v in self.items.values() if v['Name'] == '/bifrost'), None)

    def __call__(self, args, **kwargs):
        self.calls.append(args)
        if args[0] == 'cp':
            shutil.copytree(args[2].removesuffix('/.'), args[3], dirs_exist_ok=True)
            return ''
        if args[:3] == ['docker', 'image', 'inspect']:
            return json.dumps([self.images[args[3]]])
        if args[:3] == ['docker', 'container', 'ls']:
            return self.named()['Id'] if self.named() else ''
        if args[:3] == ['docker', 'ps', '-q']:
            return '\n'.join(k for k, v in self.items.items() if v['State']['Running'])
        if args[:3] == ['docker', 'container', 'inspect'] or args[:2] == ['docker', 'inspect']:
            start = 3 if args[1] == 'container' else 2
            return json.dumps([self.items[cid] for cid in args[start:]])
        if args[:2] == ['docker', 'stop']:
            self.items[args[-1]]['State'].update(Running=False, Status='exited', ExitCode=self.exit_code)
            return args[-1]
        if args[:2] == ['docker', 'update']:
            self.items[args[-1]]['HostConfig']['RestartPolicy']['Name'] = args[2].split('=', 1)[1]
            return ''
        if args[:2] == ['docker', 'rename']:
            self.items[args[2]]['Name'] = '/' + args[3]
            return ''
        if args[:2] == ['docker', 'start']:
            self.items[args[2]]['State'].update(Running=True, Status='running')
            return ''
        if args[:2] == ['docker', 'compose']:
            path = Path(args[args.index('-f') + 1])
            if 'config' in args:
                return ''
            if self.fail_start:
                raise bf.Refused('simulated startup failure')
            svc = bf.read_json(path)['services']['bifrost']
            old = self.named()
            if old:
                del self.items[old['Id']]
            cid = str(self.next_id)
            self.next_id += 1
            self.items[cid] = {'Id': cid, 'Name': '/bifrost', 'Image': svc['image'],
                'Config': {'Image': svc['image'], 'Labels': dict(svc['labels'], **{'com.docker.compose.project': bf.PROJECT})},
                'State': {'Running': True, 'Status': 'running', 'StartedAt': '2026-09-03T02:00:00Z',
                          'Health': {'Status': 'healthy'}, 'ExitCode': 0, 'OOMKilled': False},
                'HostConfig': {'RestartPolicy': {'Name': 'unless-stopped'}, 'PortBindings': {}},
                'Mounts': [{'Destination': v['target'], 'Source': v['source']} for v in svc['volumes']]}
            return ''
        if args[:2] == ['docker', 'exec']:
            if args[3] == 'sha256sum':
                src = next(m['Source'] for m in self.named()['Mounts'] if m['Destination'] == args[4])
                return bf.sha(src) + '  file'
            return json.dumps({'status': 'ok', 'components': {'db_pings': 'ok'}})
        if args[:2] == ['docker', 'logs']:
            return '{"message":"plugin status: moon - active"}'
        raise AssertionError(f'unexpected fake command {args}')


class DeploymentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.root = self.base / 'unified'
        self.root.mkdir()
        for name in ('config', 'secrets', 'releases', 'plugins', 'backups', 'data'):
            (self.root / name).mkdir()
        (self.root / 'plugins' / Path(bf.ENTRY).name).write_text('plugin18')
        profiles = {}
        for role in ('test', 'standby'):
            legacy = self.base / role
            (legacy / 'data').mkdir(parents=True)
            (legacy / 'data' / 'marker').write_text(role)
            bf.write_json(legacy / 'config.json', config(role))
            bf.atomic_write(legacy / 'postgres.env', 'BF_DB_PASSWORD=example-$-#-password\n')
            bf.atomic_write(legacy / 'compose.yaml', '{}')
            bf.write_json(self.root / 'config' / f'{role}.json', config(role))
            bf.write_json(self.root / 'config' / f'{role}.compose.json', service(role))
            bf.atomic_write(self.root / 'secrets' / f'{role}.env', 'BF_DB_PASSWORD=example-$-#-password\n')
            profiles[role] = {'legacy': str(legacy), 'legacy_project': 'bifrost-node17', 'version': '2.0.0-moon.18',
                              'source_hashes': {n: bf.sha(legacy / n) for n in ('compose.yaml', 'config.json', 'postgres.env')}}
        bf.write_json(self.root / 'config/node.json', {'ip': '10.1.12.17', 'profiles': profiles})
        bf.atomic_write(self.root / 'versions.env', 'TEST_VERSION=2.0.0-moon.18\nSTANDBY_VERSION=2.0.0-moon.18\n')
        self.docker = FakeDocker(self.root, self.base / 'test')
        for n in (18, 19):
            folder = self.root / 'releases' / f'2.0.0-moon.{n}'
            folder.mkdir()
            (folder / 'moon.so').write_text(f'plugin{n}')
            bf.write_json(folder / 'release.json', {'version': f'2.0.0-moon.{n}', 'plugin_sha256': bf.sha(folder / 'moon.so'),
                          'image_fingerprint': bf.fingerprint(self.docker.images[f'bifrost-moon:2.0.0-moon.{n}'])})
        for target, replacement in [('run', self.docker), ('local_ip', lambda: '10.1.12.17'),
                                    ('db_check', lambda *a: None), ('confirm', lambda *a: None)]:
            p = patch.object(bf, target, replacement)
            p.start()
            self.addCleanup(p.stop)
        p = patch.object(bf.os, 'chown')
        p.start()
        self.addCleanup(p.stop)
        p = patch('sys.stdout', new=io.StringIO())
        p.start()
        self.addCleanup(p.stop)

    def test_adopt_preserves_legacy_and_copies_after_stop(self):
        bf.deploy(self.root, 'test', adopt=True)
        self.assertFalse(self.docker.items['old']['State']['Running'])
        self.assertTrue(self.docker.items['old']['Name'].startswith('/bifrost-before-bfctl-'))
        self.assertEqual(self.docker.items['old']['HostConfig']['RestartPolicy']['Name'], 'no')
        self.assertEqual((self.root / 'data/test/marker').read_text(), 'test')
        stop = next(i for i, c in enumerate(self.docker.calls) if c[:2] == ['docker', 'stop'])
        copy_index = next(i for i, c in enumerate(self.docker.calls) if c[0] == 'cp')
        self.assertLess(stop, copy_index)
        self.assertFalse((self.root / 'pending.json').exists())
        self.assertEqual(bf.state(self.root)['role'], 'test')
        self.assertTrue((self.base / 'test/data/marker').exists())

    def test_mode_switch_and_rollback(self):
        bf.deploy(self.root, 'test', adopt=True)
        before = bf.state(self.root)
        bf.deploy(self.root, 'standby')
        active = bf.read_json(self.root / 'compose.yaml')['services']['bifrost']
        self.assertEqual(active['ports'][0]['published'], '8080')
        self.assertEqual(active['labels']['bifrost.deployment_role'], 'standby')
        self.assertEqual(bf.read_json(Path(bf.state(self.root)['revision']) / 'config.json'), config('standby'))
        bf.rollback(self.root)
        self.assertEqual(bf.state(self.root), before)
        self.assertEqual(bf.read_json(self.root / 'compose.yaml')['services']['bifrost']['ports'][0]['published'], '18080')

    def test_independent_versions_select_matching_plugin(self):
        bf.deploy(self.root, 'test', adopt=True)
        bf.atomic_write(self.root / 'versions.env', 'TEST_VERSION=2.0.0-moon.19\nSTANDBY_VERSION=2.0.0-moon.18\n')
        bf.deploy(self.root, 'test')
        mounts = bf.read_json(self.root / 'compose.yaml')['services']['bifrost']['volumes']
        entry = next(m for m in mounts if m['target'] == bf.ENTRY)
        self.assertTrue(entry['source'].endswith('/2.0.0-moon.19/moon.so'))
        self.assertIn('moon.18.so', entry['target'])
        bf.deploy(self.root, 'standby')
        self.assertEqual(bf.state(self.root)['version'], '2.0.0-moon.18')

    def test_start_failure_can_restore_legacy(self):
        self.docker.fail_start = True
        with self.assertRaises(bf.Refused):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue((self.root / 'pending.json').exists())
        bf.rollback(self.root)
        self.assertEqual(self.docker.named()['Id'], 'old')
        self.assertTrue(self.docker.named()['State']['Running'])
        self.assertIsNone(bf.state(self.root))
        self.assertFalse((self.root / 'compose.yaml').exists())

    def test_failed_update_can_restore_managed(self):
        bf.deploy(self.root, 'test', adopt=True)
        before = bf.state(self.root)
        self.docker.fail_start = True
        with self.assertRaises(bf.Refused):
            bf.deploy(self.root, 'standby')
        self.docker.fail_start = False
        bf.rollback(self.root)
        self.assertEqual(bf.state(self.root), before)

    def test_forced_stop_leaves_recovery_journal(self):
        self.docker.exit_code = 137
        with self.assertRaisesRegex(bf.Refused, '未正常退出'):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue((self.root / 'pending.json').exists())
        self.assertEqual(self.docker.items['old']['Name'], '/bifrost')
        self.assertFalse(any(c[0] == 'cp' for c in self.docker.calls))

    def test_wrong_role_rejected_before_stop(self):
        with self.assertRaises(bf.Refused):
            bf.deploy(self.root, 'production', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_adoption_must_not_switch_mode(self):
        with self.assertRaisesRegex(bf.Refused, '当前角色'):
            bf.deploy(self.root, 'standby', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_changed_legacy_file_blocks_adoption(self):
        (self.base / 'test/compose.yaml').write_text('changed')
        with self.assertRaisesRegex(bf.Refused, 'init 后发生变化'):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_tampered_plugin_blocks_stop(self):
        (self.root / 'releases/2.0.0-moon.18/moon.so').write_text('tampered')
        with self.assertRaisesRegex(bf.Refused, 'SHA-256 校验失败'):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_pending_blocks_second_operation(self):
        bf.write_json(self.root / 'pending.json', {})
        with self.assertRaisesRegex(bf.Refused, '未完成操作'):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_interrupted_start_is_recoverable(self):
        original = self.docker

        def interrupted(args, **kwargs):
            if args[:2] == ['docker', 'compose'] and 'up' in args:
                raise KeyboardInterrupt()
            return original(args, **kwargs)

        with patch.object(bf, 'run', side_effect=interrupted):
            with self.assertRaises(KeyboardInterrupt):
                bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue((self.root / 'pending.json').exists())
        bf.rollback(self.root)
        self.assertEqual(self.docker.named()['Id'], 'old')

    def test_failed_verification_can_restore_legacy(self):
        original = self.docker

        def no_plugin(args, **kwargs):
            if args[:2] == ['docker', 'logs']:
                return 'plugin failed to load'
            return original(args, **kwargs)

        with patch.object(bf, 'run', side_effect=no_plugin):
            with self.assertRaisesRegex(bf.Refused, 'Moon 启动成功证据'):
                bf.deploy(self.root, 'test', adopt=True)
        self.assertTrue(self.docker.named()['State']['Running'])
        self.assertTrue((self.root / 'pending.json').exists())
        bf.rollback(self.root)
        self.assertEqual(self.docker.named()['Id'], 'old')

    def test_other_legacy_data_writer_blocks_copy(self):
        other = copy.deepcopy(self.docker.items['old'])
        other.update(Id='other', Name='/unexpected-writer', Mounts=[{'Source': str(self.base / 'test/data'), 'RW': True}])
        self.docker.items['other'] = other
        with self.assertRaisesRegex(bf.Refused, '可写方式挂载旧数据目录'):
            bf.deploy(self.root, 'test', adopt=True)
        self.assertFalse(any(c[0] == 'cp' for c in self.docker.calls))

    def test_data_copy_only_happens_once_per_mode(self):
        bf.deploy(self.root, 'test', adopt=True)
        bf.deploy(self.root, 'standby')
        (self.base / 'test/data/marker').write_text('stale old deployment')
        bf.deploy(self.root, 'test')
        self.assertEqual((self.root / 'data/test/marker').read_text(), 'test')

    def test_startup_receipt_survives_log_rotation_but_not_restart(self):
        bf.deploy(self.root, 'test', adopt=True)
        original = self.docker

        def rotated_logs(args, **kwargs):
            if args[:2] == ['docker', 'logs']:
                return 'only recent request logs remain'
            return original(args, **kwargs)

        with patch.object(bf, 'run', side_effect=rotated_logs):
            bf.verify(self.root, bf.state(self.root))
            self.docker.named()['State']['StartedAt'] = '2026-09-03T03:00:00Z'
            with self.assertRaisesRegex(bf.Refused, 'Moon 启动成功证据'):
                bf.verify(self.root, bf.state(self.root))

    def test_cancel_never_stops_service(self):
        with patch.object(bf, 'confirm', side_effect=bf.Refused('cancelled')):
            with self.assertRaises(bf.Refused):
                bf.deploy(self.root, 'test', adopt=True)
        self.assertFalse((self.root / 'pending.json').exists())
        self.assertTrue(self.docker.named()['State']['Running'])

    def test_verify_rejects_modified_revision(self):
        bf.deploy(self.root, 'test', adopt=True)
        revision = Path(bf.state(self.root)['revision'])
        (revision / 'config.json').write_text('{}')
        with self.assertRaisesRegex(bf.Refused, '已被修改'):
            bf.verify(self.root, bf.state(self.root))

    def test_readoption_copies_fresh_legacy_data(self):
        bf.deploy(self.root, 'test', adopt=True)
        bf.rollback(self.root)
        (self.base / 'test/data/marker').write_text('new legacy state')
        bf.deploy(self.root, 'test', adopt=True)
        self.assertEqual((self.root / 'data/test/marker').read_text(), 'new legacy state')


class ValidationTest(unittest.TestCase):
    def test_chinese_help_labels(self):
        parser = bf.ChineseArgumentParser(description='测试')
        parser.add_argument('command')
        help_text = parser.format_help()
        self.assertIn('命令与参数', help_text)
        self.assertIn('选项', help_text)
        self.assertIn('显示帮助并退出', help_text)

    def test_argument_errors_are_chinese(self):
        parser = bf.ChineseArgumentParser(prog='tool')
        parser.add_argument('role', choices=('test', 'production'))
        for arguments, expected in [([], '缺少必填参数'), (['wrong'], '参数值不在允许范围')]:
            error_output = io.StringIO()
            with self.subTest(arguments=arguments), patch('sys.stderr', new=error_output), self.assertRaises(SystemExit):
                parser.parse_args(arguments)
            rendered = error_output.getvalue()
            self.assertIn('用法：', rendered)
            self.assertIn(expected, rendered)
            self.assertNotIn('usage:', rendered)
            self.assertNotIn('error:', rendered)

    def test_missing_release_reports_actionable_chinese_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'releases').mkdir()
            with self.assertRaisesRegex(bf.Refused, '目标版本.*尚未导入'):
                bf.release(root, '2.0.0-moon.99')

    def test_raw_env_file_is_validated_from_compose_source(self):
        valid = """services:
  bifrost:
    env_file:
      - path: ./postgres.env
        format: raw
    environment:
      APP_PORT: \"8080\"
"""
        self.assertTrue(bf.has_raw_postgres_env(valid))
        self.assertNotIn('BF_DB_PASSWORD', valid)
        self.assertFalse(bf.has_raw_postgres_env(valid.replace('format: raw', 'format: dotenv')))
        self.assertFalse(bf.has_raw_postgres_env(valid.replace('./postgres.env', './other.env')))
        self.assertFalse(bf.has_raw_postgres_env(valid + '\n    env_file:\n      - ./postgres.env\n'))
        normalized = bf.normalize_legacy_service(
            {'env_file': None, 'environment': {'APP_PORT': '8080', 'BF_DB_PASSWORD': 'must-not-persist'}}, valid)
        self.assertNotIn('env_file', normalized)
        self.assertEqual(normalized['environment'], {'APP_PORT': '8080'})
        self.assertNotIn('must-not-persist', json.dumps(normalized))

    def test_all_database_roles(self):
        for role in bf.ROLES:
            bf.check_config(config(role), role)
        with self.assertRaises(bf.Refused):
            bf.check_config(config('production'), 'test')

    def test_extra_dynamic_config_rejected(self):
        cfg = config('test')
        cfg['providers'] = {}
        with self.assertRaises(bf.Refused):
            bf.check_config(cfg, 'test')

    def test_version_rejects_shell_and_path_syntax(self):
        for ver in ('../x', 'latest', '2.0.0-moon.19;reboot', '$(id)', '2.0.0-moon.19\n'):
            with self.subTest(ver=ver), self.assertRaises(bf.Refused):
                bf.version(ver)

    def test_ports_not_inferred_from_current_environment(self):
        with self.assertRaises(bf.Refused):
            bf.check_ports(service('standby'), 'test')

    def test_fingerprint_ignores_id_store_differences(self):
        a = {'Id': 'manifest-id', 'Config': {'User': '1000:0'}, 'RootFS': {'Layers': ['a']}}
        b = dict(a, Id='config-id')
        self.assertEqual(bf.fingerprint(a), bf.fingerprint(b))
        b['RootFS'] = {'Layers': ['b']}
        self.assertNotEqual(bf.fingerprint(a), bf.fingerprint(b))

    def test_password_raw_and_permissions(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'secret'
            bf.atomic_write(path, 'BF_DB_PASSWORD=some$#"value\n')
            self.assertEqual(bf.password(path), 'some$#"value')
            path.chmod(0o644)
            with self.assertRaises(bf.Refused):
                bf.password(path)

    def test_run_withholds_failure_secrets(self):
        with self.assertRaises(bf.Refused) as ctx:
            bf.run(['sh', '-c', 'echo secret-value >&2; exit 1'])
        self.assertNotIn('secret-value', str(ctx.exception))

    def test_check_tree_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)
            (path / 'link').symlink_to('/etc')
            with self.assertRaises(bf.Refused):
                bf.check_tree(path)

    def test_logs_capture_stderr(self):
        output = bf.run(['sh', '-c', 'echo plugin-evidence >&2'], merge_stderr=True)
        self.assertIn('plugin-evidence', output)

    def test_psql_is_read_only_and_uses_matching_credentials(self):
        calls = []

        def query(args, **kwargs):
            calls.append((args, kwargs))
            sql = args[-1]
            if 'current_database' in sql:
                suffix = 'config' if 'dbname=bifrost_test_config' in args[1] else 'logs'
                return f'bifrost_test_{suffix}|bf_test|true\n'
            if 'SELECT path' in sql:
                return bf.ENTRY + '\n'
            if 'COUNT' in sql:
                return '0\n'
            return 't\n'

        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'secret'
            bf.atomic_write(path, 'BF_DB_PASSWORD=example-only\n')
            with patch.object(bf, 'run', side_effect=query):
                bf.db_check(config('test'), path, 'test')
        self.assertEqual(len(calls), 5)
        for args, kwargs in calls:
            self.assertNotIn('example-only', ' '.join(args))
            self.assertIn('default_transaction_read_only=on', kwargs['env']['PGOPTIONS'])
            self.assertEqual(kwargs['env']['PGPASSWORD'], 'example-only')

    def test_versions_file_does_not_execute_shell(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            bf.atomic_write(root / 'versions.env', 'TEST_VERSION=$(touch bad-file)\n')
            with self.assertRaises(bf.Refused):
                bf.read_versions(root)
            bf.atomic_write(root / 'versions.env', 'TEST_VERSION=2.0.0-moon.18\nTEST_VERSION=2.0.0-moon.19\n')
            with self.assertRaises(bf.Refused):
                bf.read_versions(root)


class NewNodeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / 'bifrost'
        self.root.mkdir()
        self.stdout = patch('sys.stdout', new=io.StringIO())
        self.stdout.start()
        self.addCleanup(self.stdout.stop)

    def test_bootstrap_production_creates_standard_profile_without_legacy_paths(self):
        args = bf.argparse.Namespace(node_type='production', ip='10.2.3.4', version='2.0.0-moon.19')
        with patch.object(bf, 'global_ips', return_value={'10.2.3.4'}), patch.object(bf, 'container', return_value=None), \
                patch.object(bf, 'read_secret', return_value='new-node-secret'), \
                patch.object(bf, 'db_check') as database:
            bf.bootstrap(self.root, args)
        node = bf.read_json(self.root / 'config/node.json')
        self.assertEqual(set(node['profiles']), {'production'})
        self.assertNotIn('legacy', json.dumps(node))
        self.assertEqual((self.root / 'versions.env').read_text(), 'PRODUCTION_VERSION=2.0.0-moon.19\n')
        self.assertEqual(bf.password(self.root / 'secrets/production.env'), 'new-node-secret')
        self.assertEqual(self.root.joinpath('plugins').stat().st_mode & 0o777, 0o750)
        bf.check_ports(bf.read_json(self.root / 'config/production.compose.json'), 'production')
        database.assert_called_once()

    def test_bootstrap_dual_role_uses_separate_passwords_and_ports(self):
        args = bf.argparse.Namespace(node_type='test-standby', ip='10.2.3.5', version='2.0.0-moon.19')
        with patch.object(bf, 'global_ips', return_value={'10.2.3.5'}), patch.object(bf, 'container', return_value=None), \
                patch.object(bf, 'read_secret', side_effect=['test-password', 'prod-password']), \
                patch.object(bf, 'db_check') as database:
            bf.bootstrap(self.root, args)
        self.assertEqual(bf.password(self.root / 'secrets/test.env'), 'test-password')
        self.assertEqual(bf.password(self.root / 'secrets/standby.env'), 'prod-password')
        self.assertEqual(bf.read_json(self.root / 'config/test.compose.json')['ports'][0]['published'], '18080')
        self.assertEqual(bf.read_json(self.root / 'config/standby.compose.json')['ports'][0]['published'], '8080')
        self.assertEqual(database.call_count, 2)

    def test_bootstrap_rejects_unassigned_ip_before_password_prompt(self):
        args = bf.argparse.Namespace(node_type='production', ip='10.2.3.9', version='2.0.0-moon.19')
        with patch.object(bf, 'global_ips', return_value={'10.2.3.4'}), patch.object(bf, 'container', return_value=None), patch.object(bf, 'read_secret') as secret:
            with self.assertRaisesRegex(bf.Refused, '不属于当前主机'):
                bf.bootstrap(self.root, args)
        secret.assert_not_called()

    def test_bootstrap_partial_path_does_not_create_more_paths(self):
        (self.root / 'backups').mkdir()
        args = bf.argparse.Namespace(node_type='production', ip='10.2.3.4', version='2.0.0-moon.19')
        with patch.object(bf, 'global_ips', return_value={'10.2.3.4'}), patch.object(bf, 'container', return_value=None), \
                patch.object(bf, 'read_secret', return_value='new-node-secret'), patch.object(bf, 'db_check'):
            with self.assertRaisesRegex(bf.Refused, '未完成的 bootstrap'):
                bf.bootstrap(self.root, args)
        self.assertFalse((self.root / 'config').exists())

    def test_new_node_failed_first_launch_can_rollback_and_retry(self):
        args = bf.argparse.Namespace(node_type='production', ip='10.2.3.4', version='2.0.0-moon.19')
        with patch.object(bf, 'global_ips', return_value={'10.2.3.4'}), patch.object(bf, 'container', return_value=None), \
                patch.object(bf, 'read_secret', return_value='new-node-secret'), patch.object(bf, 'db_check'):
            bf.bootstrap(self.root, args)
        docker = FakeDocker(self.root, self.root)
        docker.items = {}
        release = self.root / 'releases/2.0.0-moon.19'
        release.mkdir()
        (release / 'moon.so').write_text('plugin19')
        (self.root / 'plugins' / Path(bf.ENTRY).name).write_text('placeholder')
        bf.write_json(release / 'release.json', {'version': '2.0.0-moon.19', 'plugin_sha256': bf.sha(release / 'moon.so'),
                      'image_fingerprint': bf.fingerprint(docker.images['bifrost-moon:2.0.0-moon.19'])})
        patches = [patch.object(bf, 'run', docker), patch.object(bf, 'global_ips', return_value={'10.2.3.4'}),
                   patch.object(bf, 'db_check'), patch.object(bf, 'confirm'), patch.object(bf.os, 'chown')]
        for item in patches:
            item.start(); self.addCleanup(item.stop)
        docker.fail_start = True
        with self.assertRaises(bf.Refused):
            bf.launch(self.root, 'production')
        self.assertTrue((self.root / 'pending.json').exists())
        docker.fail_start = False
        bf.rollback(self.root)
        self.assertIsNone(docker.named())
        self.assertFalse((self.root / 'state.json').exists())
        self.assertFalse((self.root / 'data/production').exists())
        bf.launch(self.root, 'production')
        self.assertEqual(bf.state(self.root)['role'], 'production')
        self.assertEqual(docker.named()['Config']['Labels']['bifrost.managed_by'], 'bifrost-deploy')


class PackageTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.root = self.base / 'node'
        (self.root / 'releases').mkdir(parents=True)
        (self.root / 'plugins').mkdir()
        self.bundle = self.base / 'bundle'
        self.bundle.mkdir()
        (self.bundle / 'moon.so').write_bytes(b'plugin-fixture')
        (self.bundle / 'image.tar').write_bytes(b'archive-fixture')
        self.info = {'Os': 'linux', 'Architecture': 'amd64', 'Id': 'local-image', 'Config': {}, 'RootFS': {'Layers': ['layer']}}
        self.manifest = {'version': '2.0.0-moon.19', 'image_fingerprint': bf.fingerprint(self.info),
                         'plugin_sha256': bf.sha(self.bundle / 'moon.so'), 'archive_sha256': bf.sha(self.bundle / 'image.tar')}
        bf.write_json(self.bundle / 'release.json', self.manifest)
        p = patch('sys.stdout', new=io.StringIO())
        p.start()
        self.addCleanup(p.stop)

    def test_import_loads_once_and_does_not_restart(self):
        with patch.object(bf, 'run', return_value='') as runner, patch.object(bf, 'image_info', return_value=self.info):
            bf.import_bundle(self.root, bf.argparse.Namespace(bundle=str(self.bundle)))
        calls = [c.args[0] for c in runner.call_args_list]
        self.assertTrue(any(c[:3] == ['docker', 'image', 'load'] for c in calls))
        self.assertFalse(any('stop' in c or 'compose' in c for c in calls))
        self.assertEqual(bf.read_json(self.root / 'releases/2.0.0-moon.19/release.json'), self.manifest)
        with self.assertRaisesRegex(bf.Refused, '已登记'):
            bf.import_bundle(self.root, bf.argparse.Namespace(bundle=str(self.bundle)))

    def test_bad_archive_rejected_before_docker(self):
        (self.bundle / 'image.tar').write_bytes(b'bad')
        with patch.object(bf, 'run') as runner, self.assertRaisesRegex(bf.Refused, '镜像归档 SHA-256 校验失败'):
            bf.import_bundle(self.root, bf.argparse.Namespace(bundle=str(self.bundle)))
        runner.assert_not_called()

    def test_conflicting_tag_is_not_overwritten(self):
        other = dict(self.info, Config={'User': 'different'})
        with patch.object(bf, 'run', return_value='existing-id') as runner, patch.object(bf, 'image_info', return_value=other):
            with self.assertRaisesRegex(bf.Refused, '内容不同'):
                bf.import_bundle(self.root, bf.argparse.Namespace(bundle=str(self.bundle)))
        self.assertFalse(any(c.args[0][:3] == ['docker', 'image', 'load'] for c in runner.call_args_list))

    def test_pack_outputs_immutable_hash_manifest(self):
        output = self.base / 'packed'

        def save(args, **kwargs):
            self.assertEqual(args[:3], ['docker', 'image', 'save'])
            Path(args[4]).write_bytes(b'saved-image')
            return ''

        args = bf.argparse.Namespace(version='2.0.0-moon.19', plugin=str(self.bundle / 'moon.so'), output=str(output))
        with patch.object(bf, 'run', side_effect=save), patch.object(bf, 'image_info', return_value=self.info):
            bf.pack(args)
            with self.assertRaisesRegex(bf.Refused, '输出目录已存在'):
                bf.pack(args)
        manifest = bf.read_json(output / 'release.json')
        self.assertEqual(manifest['archive_sha256'], bf.sha(output / 'image.tar'))
        self.assertEqual(manifest['plugin_sha256'], bf.sha(output / 'moon.so'))


@unittest.skipUnless(shutil.which('docker'), 'Docker Compose CLI not installed')
class ComposeTest(unittest.TestCase):
    """Real Compose parsing only: no daemon or external network used."""

    def test_init_and_generated_compose_parse(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / 'unified'
            root.mkdir()
            plugins = Path(tmp) / 'plugins'
            plugins.mkdir()
            (plugins / Path(bf.ENTRY).name).write_text('fixture-plugin')
            profiles = {}
            for role in ('test', 'standby'):
                legacy = Path(tmp) / role
                (legacy / 'data').mkdir(parents=True)
                bf.write_json(legacy / 'config.json', config(role))
                bf.atomic_write(legacy / 'postgres.env', 'BF_DB_PASSWORD=fixture$#"value\n')
                port = '18080' if role == 'test' else '8080'
                template = f"""name: bifrost-node17
services:
  bifrost:
    container_name: bifrost
    image: bifrost-moon:2.0.0-moon.18
    ports:
      - \"{port}:8080\"
    env_file:
      - path: ./postgres.env
        format: raw
    environment:
      APP_PORT: \"8080\"
    volumes:
      - type: bind
        source: ./data
        target: /app/data
      - type: bind
        source: ./config.json
        target: /app/data/config.json
        read_only: true
      - type: bind
        source: {plugins}
        target: /app/data/plugins
        read_only: true
"""
                bf.atomic_write(legacy / 'compose.yaml', template)
                profiles[role] = str(legacy)
            info = {'Id': 'sha256:' + 'a' * 64, 'Os': 'linux', 'Architecture': 'amd64', 'Config': {}, 'RootFS': {}, 'Created': 'today'}
            with patch.object(bf, 'LEGACY', {'10.1.12.17': profiles}), patch.object(bf, 'local_ip', return_value='10.1.12.17'), \
                    patch.object(bf, 'PLUGIN_DIR', plugins), patch.object(bf, 'image_info', return_value=info), \
                    patch('sys.stdout', new=io.StringIO()):
                bf.init(root)
            for role in profiles:
                svc = bf.read_json(root / 'config' / f'{role}.compose.json')
                revision = root / 'backups' / role
                revision.mkdir()
                bf.atomic_write(revision / 'postgres.env', 'BF_DB_PASSWORD=fixture$#"value\n')
                cfg = bf.build_compose(root, role, '2.0.0-moon.18', info['Id'], revision, svc)
                bf.write_json(revision / 'compose.yaml', cfg)
                result = json.loads(bf.compose(revision / 'compose.yaml', 'config', '--format', 'json'))
                active = result['services']['bifrost']
                self.assertEqual(result['name'], bf.PROJECT)
                # `compose config` escapes literal dollars so its output can be reused as Compose input.
                self.assertEqual(active['environment']['BF_DB_PASSWORD'], 'fixture$$#"value')
                self.assertEqual(active['image'], info['Id'])
                self.assertTrue(all(m['source'].startswith(str(root)) for m in active['volumes']))
                self.assertEqual(active['ports'][0]['published'], '18080' if role == 'test' else '8080')
            self.assertFalse((root / 'state.json').exists())

    def test_new_node_standard_services_parse(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in ('data', 'plugins', 'releases'):
                (root / name).mkdir()
            for role in bf.ROLES:
                revision = root / f'revision-{role}'
                revision.mkdir()
                bf.atomic_write(revision / 'postgres.env', 'BF_DB_PASSWORD=fixture$#"value\n')
                bf.write_json(revision / 'config.json', config(role))
                document = bf.build_compose(root, role, '2.0.0-moon.19', 'sha256:' + 'a' * 64,
                                            revision, bf.standard_service(role))
                bf.write_json(revision / 'compose.yaml', document)
                rendered = json.loads(bf.compose(revision / 'compose.yaml', 'config', '--format', 'json'))
                service = rendered['services']['bifrost']
                self.assertEqual(service['ports'][0]['published'], '18080' if role == 'test' else '8080')
                if role == 'test':
                    self.assertNotIn('extra_hosts', service)
                else:
                    self.assertIn('langfuse-archive.tailb34b09.ts.net=100.108.96.112', service['extra_hosts'])


if __name__ == '__main__':
    unittest.main()
