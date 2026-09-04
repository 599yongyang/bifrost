#!/usr/bin/env python3
"""Seed EMPTY test config from a consistent production PG snapshot.

Source is read-only. Default checks only. --apply creates a private filtered
archive and restores + sanitizes test config in one transaction. Test logs stay
empty for Bifrost to initialize later. No existing tables are dropped/replaced.
"""
import argparse
import getpass
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

EXCLUDE = {
    "sessions", "distributed_locks", "alert_cooldowns", "sidekiq", "batch_jobs", "webhook_jobs",
    "notifications", "temp_tokens", "config_log_store", "prompt_sessions", "prompt_session_messages",
    "oauth_user_sessions", "oauth2_authorize_requests", "oauth2_refresh_tokens", "mcp_oauth_flows",
    "mcp_per_user_header_flows", "oauth_tokens", "oauth_user_tokens", "mcp_oauth_tokens",
    "mcp_per_user_header_credentials",
}


def env(password, readonly=False):
    result = {k: v for k, v in os.environ.items() if not k.startswith("PG")}
    result.update(PGPASSWORD=password, PGSSLMODE="require", PGCONNECT_TIMEOUT="5",
                  PGAPPNAME="bifrost-test-config-seed",
                  PGOPTIONS="-c lock_timeout=5000 -c statement_timeout=120000" +
                  (" -c default_transaction_read_only=on" if readonly else ""))
    return result


def connection(user, database):
    return ["-h", "10.1.12.7", "-p", "5432", "-U", user, "-d", database, "-w"]


def command(args, *, password=None, readonly=False, text=True, **kwargs):
    result = subprocess.run(args, env=env(password, readonly) if password is not None else None,
                            text=text, stderr=subprocess.PIPE, timeout=300, **kwargs)
    if result.returncode:
        raise RuntimeError("Command failed: " + args[0] + " (exit " + str(result.returncode) +
                           "). Do not start test Bifrost or clear databases; inspect state first. Details withheld to protect secrets.")
    return result


def query(user, database, password, sql):
    result = command(["psql", *connection(user, database), "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"],
                     password=password, readonly=True, input=sql, stdout=subprocess.PIPE)
    return json.loads(result.stdout)


def check_target(database, password):
    info = query("bf_test", database, password, """SELECT json_build_object(
 'db',current_database(),'user',current_user,'host',inet_server_addr(),
 'ssl',(SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()),
 'objects',(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S','f')),
 'connections',(SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()));""")
    if (info["db"], info["user"], info["host"], info["ssl"]) != (database, "bf_test", "10.1.12.7", True):
        raise RuntimeError("Wrong test database/account/TLS state")
    if info["objects"] or info["connections"]:
        raise RuntimeError(database + " has existing objects or connections; no overwrite permitted")


def filter_toc(text):
    result, removed = [], 0
    for line in text.splitlines():
        if re.match(r"^\d+;\s+\d+\s+\d+\s+SCHEMA\s+-\s+public(?:\s|$)", line):
            removed += 1
            continue
        result.append(line)
    if removed != 1:
        raise RuntimeError("Unexpected archive public-schema entry; review restore plan")
    return "\n".join(result) + "\n"


def copy_counts(path):
    counts, table = {}, None
    with path.open(encoding="utf-8") as stream:
        for line in stream:
            if table:
                if line.rstrip("\r\n") == r"\.":
                    table = None
                else:
                    counts[table] += 1
            else:
                match = re.match(r'^COPY public\.([a-z_][a-z_0-9]*) \(.*\) FROM stdin;\s*$', line)
                if match:
                    table = match[1]
                    if table in counts:
                        raise RuntimeError("Repeated COPY table in archive")
                    counts[table] = 0
    if table:
        raise RuntimeError("Incomplete archive COPY data")
    if "migrations" not in counts or any(counts.get(t, 0) for t in EXCLUDE):
        raise RuntimeError("Missing migrations or excluded runtime records in archive")
    return counts


GUARD_SQL = """DO $guard$ BEGIN
IF current_database() IS DISTINCT FROM 'bifrost_test_config' OR current_user IS DISTINCT FROM 'bf_test'
 OR inet_server_addr() IS DISTINCT FROM '10.1.12.7'::inet THEN RAISE EXCEPTION 'Wrong test target'; END IF;
IF NOT pg_try_advisory_xact_lock(6842026090317) THEN RAISE EXCEPTION 'Another test seed is active'; END IF;
IF EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S','f')) THEN RAISE EXCEPTION 'Test target is not empty'; END IF;
END $guard$;
"""


def sanitize_sql(counts):
    result = [
        "UPDATE public.alert_channels SET enabled=false;",
        "UPDATE public.alert_rules SET enabled=false;",
        "UPDATE public.daily_report_settings SET enabled=false,internal_enabled=false,external_enabled=false;",
        "UPDATE public.config_webhook_endpoints SET disabled=true;",
        "UPDATE public.config_vector_store SET enabled=false;",
        "UPDATE public.config_mcp_clients SET disabled=true;",
        "UPDATE public.config_plugins SET enabled=false WHERE name IN ('otel','maxim','semantic_cache','semanticcache');",
        "DO $verify$ DECLARE r record; bad boolean; BEGIN",
    ]
    for table, number in sorted(counts.items()):
        if not re.fullmatch(r"[a-z_][a-z_0-9]*", table) or not isinstance(number, int) or number < 0:
            raise RuntimeError("Invalid archive table/count")
        result.append(f"IF (SELECT count(*) FROM public.\"{table}\") <> {number} THEN RAISE EXCEPTION 'Snapshot row count mismatch'; END IF;")
    for table in sorted(EXCLUDE):
        result.append(f"IF EXISTS(SELECT 1 FROM public.\"{table}\") THEN RAISE EXCEPTION 'Runtime records were copied'; END IF;")
    result += [
        "FOR r IN SELECT table_name FROM information_schema.columns WHERE table_schema='public' AND column_name='encryption_status' LOOP",
        "EXECUTE format('SELECT EXISTS(SELECT 1 FROM public.%I WHERE encryption_status IS NOT NULL AND encryption_status NOT IN (''plain_text'',''''))',r.table_name) INTO bad;",
        "IF bad THEN RAISE EXCEPTION 'Encrypted config requires a separate key review'; END IF; END LOOP;",
        "END $verify$;",
        "SELECT 'TEST_SNAPSHOT_VALIDATED';",
    ]
    return "\n".join(result) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    if not sys.stdin.isatty():
        raise RuntimeError("Run interactively; do not pipe passwords")
    prod = getpass.getpass("bf_prod password (source, read only): ")
    test = getpass.getpass("bf_test password (test destination): ")
    check_target("bifrost_test_config", test)
    check_target("bifrost_test_logs", test)
    source = query("bf_prod", "bifrost_prod_config", prod, """SELECT json_build_object(
 'db',current_database(),'user',current_user,'host',inet_server_addr(),
 'ssl',(SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()),
 'tables',(SELECT json_agg(c.relname ORDER BY c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relkind IN ('r','p')));""")
    if (source["db"], source["user"], source["host"], source["ssl"]) != ("bifrost_prod_config", "bf_prod", "10.1.12.7", True):
        raise RuntimeError("Wrong production source identity")
    if len(source["tables"]) != 62 or not EXCLUDE <= set(source["tables"]):
        raise RuntimeError("Production table set changed; review clone policy first")
    print("Production source: 62 config tables. Both test databases are empty and idle.")
    if not args.apply:
        print("CHECK_OK: no archive created and no database writes performed.")
        return
    work = Path(tempfile.mkdtemp(prefix="test-config-seed-", dir="/opt/bifrost-migration"))
    os.umask(0o077)
    print("Private snapshot directory (do not share):", work, flush=True)
    archive = work / "config-snapshot.dump"
    dump_args = ["pg_dump", *connection("bf_prod", "bifrost_prod_config"), "-Fc", "--schema=public",
                 "--no-owner", "--no-privileges", "--lock-wait-timeout=5s"]
    dump_args += ["--exclude-table-data=public." + t for t in sorted(EXCLUDE)]
    with archive.open("xb") as output:
        command(dump_args, password=prod, readonly=True, text=False, stdout=output)
    toc = command(["pg_restore", "--list", str(archive)], stdout=subprocess.PIPE).stdout
    toc_path = work / "restore.list"
    toc_path.write_text(filter_toc(toc))
    restore_path = work / "restore.sql"
    command(["pg_restore", "--no-owner", "--no-privileges", "--use-list", str(toc_path),
             "--file", str(restore_path), str(archive)], stdout=subprocess.DEVNULL)
    counts = copy_counts(restore_path)
    guard, safety = work / "guard.sql", work / "test-safety.sql"
    guard.write_text(GUARD_SQL)
    safety.write_text(sanitize_sql(counts))
    check_target("bifrost_test_config", test)
    check_target("bifrost_test_logs", test)
    print("Restoring and disabling test-side integrations in one transaction...", flush=True)
    command(["psql", *connection("bf_test", "bifrost_test_config"), "-X", "-q", "-1", "-v", "ON_ERROR_STOP=1",
             "-v", "VERBOSITY=sqlstate", "-f", str(guard), "-f", str(restore_path), "-f", str(safety)],
            password=test, stdout=subprocess.PIPE)
    summary = query("bf_test", "bifrost_test_config", test, """SELECT json_build_object(
 'providers',(SELECT count(*) FROM config_providers),'keys',(SELECT count(*) FROM config_keys),
 'virtual_keys',(SELECT count(*) FROM governance_virtual_keys),'routing_rules',(SELECT count(*) FROM routing_rules),
 'moon_enabled',(SELECT enabled FROM config_plugins WHERE name='moon'),
 'otel_enabled',(SELECT enabled FROM config_plugins WHERE name='otel'));
""")
    print("TEST_CONFIG_READY:", json.dumps(summary, ensure_ascii=False))
    print("Test logs are still empty; Bifrost initializes them later. Old SQLite and production were not changed.")
    print("This remains real provider configuration: test inference may call/charge real suppliers. Do not route customer traffic to it.")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, subprocess.SubprocessError, ValueError, TypeError) as exc:
        print("STOP:", str(exc) if isinstance(exc, RuntimeError) else type(exc).__name__, file=sys.stderr)
        sys.exit(1)
