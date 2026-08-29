import docs from "./zh/docs";
import core from "./zh/core";
import supplemental from "./zh/supplemental";
import observability from "./zh/workspace/observability";
import analytics from "./zh/workspace/analytics";
import enterprise from "./zh/workspace/enterprise";
import prompts from "./zh/workspace/prompts";
import governance from "./zh/workspace/governance";
import providers from "./zh/workspace/providers";
import catalog from "./zh/workspace/catalog";
import alerting from "./zh/workspace/alerting";
import routing from "./zh/workspace/routing";
import virtualKeys from "./zh/workspace/virtualKeys";
import config from "./zh/workspace/config";
import pricing from "./zh/workspace/pricing";
import mcp from "./zh/workspace/mcp";
import plugins from "./zh/workspace/plugins";

const zh = {
	docs,
	...core,
	supplemental,
	workspace: {
		...observability,
		...analytics,
		...enterprise,
		...prompts,
		...governance,
		...providers,
		...catalog,
		...alerting,
		...routing,
		...virtualKeys,
		...config,
		...pricing,
		...mcp,
		...plugins,
	},
};

export default zh;