import docs from "./en/docs";
import core from "./en/core";
import supplemental from "./en/supplemental";
import observability from "./en/workspace/observability";
import analytics from "./en/workspace/analytics";
import enterprise from "./en/workspace/enterprise";
import prompts from "./en/workspace/prompts";
import governance from "./en/workspace/governance";
import providers from "./en/workspace/providers";
import catalog from "./en/workspace/catalog";
import alerting from "./en/workspace/alerting";
import routing from "./en/workspace/routing";
import virtualKeys from "./en/workspace/virtualKeys";
import config from "./en/workspace/config";
import pricing from "./en/workspace/pricing";
import mcp from "./en/workspace/mcp";
import plugins from "./en/workspace/plugins";

const en = {
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

export default en;