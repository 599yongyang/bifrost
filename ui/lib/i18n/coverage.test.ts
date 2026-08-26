import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const visibleAttributes = new Set(["placeholder", "title", "aria-label", "alt"]);
const properNames = new Set([
	"Bifrost",
	"Python",
	"TypeScript",
	"OpenAI SDK",
	"Anthropic SDK",
	"Google GenAI SDK",
	"LiteLLM SDK",
	"LangChain SDK",
]);
// Ratchet only: the initial localization PR predates several v1.6.10 screens,
// so remaining dynamic prose is reduced incrementally. New raw English must
// never increase this reviewed baseline.
const rawEnglishBaseline = 1227;

function tsxFiles(directory: string): string[] {
	return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
		const fullPath = path.join(directory, entry.name);
		if (entry.isDirectory()) return tsxFiles(fullPath);
		return entry.isFile() && entry.name.endsWith(".tsx") ? [fullPath] : [];
	});
}

function looksUserFacing(value: string): boolean {
	const text = value.replace(/\s+/g, " ").trim();
	if (!/[A-Za-z]{2}/.test(text) || properNames.has(text)) return false;
	if (/^(https?:|data:|urn:|env\.|sk-|[A-Z0-9_.:/%+-]{1,20}$)/.test(text)) return false;
	return true;
}

function rawEnglishNodes(file: string, root: string): string[] {
	const text = fs.readFileSync(file, "utf8");
	const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
	const found: string[] = [];
	const note = (node: ts.Node, value: string) => {
		if (!looksUserFacing(value)) return;
		const line = source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
		found.push(`${path.relative(root, file).split(path.sep).join("/")}:${line} ${value.replace(/\s+/g, " ").trim()}`);
	};
	const isVisibleJsxExpression = (node: ts.Node): boolean => {
		let current: ts.Node | undefined = node.parent;
		while (current && !ts.isJsxExpression(current)) {
			if (ts.isStatement(current)) return false;
			current = current.parent;
		}
		if (!current) return false;
		return !ts.isJsxAttribute(current.parent) || (ts.isIdentifier(current.parent.name) && visibleAttributes.has(current.parent.name.text));
	};
	const visit = (node: ts.Node) => {
		if (ts.isJsxText(node)) {
			const parentTag = ts.isJsxElement(node.parent) ? node.parent.openingElement.tagName.getText(source) : "";
			if (parentTag !== "code") note(node, node.getText(source));
		} else if (
			ts.isJsxAttribute(node) &&
			ts.isIdentifier(node.name) &&
			visibleAttributes.has(node.name.text) &&
			node.initializer &&
			ts.isStringLiteral(node.initializer)
		) {
			note(node.initializer, node.initializer.text);
		} else if (ts.isConditionalExpression(node) && isVisibleJsxExpression(node)) {
			if (ts.isStringLiteral(node.whenTrue)) note(node.whenTrue, node.whenTrue.text);
			if (ts.isStringLiteral(node.whenFalse)) note(node.whenFalse, node.whenFalse.text);
		} else if (ts.isCallExpression(node) && node.arguments.length && ts.isStringLiteral(node.arguments[0])) {
			if (/^(toast\.(success|error|info|warning)|confirm|alert)$/.test(node.expression.getText(source)))
				note(node.arguments[0], node.arguments[0].text);
		}
		ts.forEachChild(node, visit);
	};
	visit(source);
	return found;
}

describe("i18n component coverage", () => {
	it("does not increase raw user-facing English nodes", () => {
		const root = process.cwd();
		const offenders = [...tsxFiles(path.join(root, "app")), ...tsxFiles(path.join(root, "components"))].flatMap((file) =>
			rawEnglishNodes(file, root),
		);
		expect(offenders.length, offenders.slice(0, 30).join("\n")).toBeLessThanOrEqual(rawEnglishBaseline);
	});

	it("fully localizes profiler surfaces touched by this change", () => {
		const root = process.cwd();
		const offenders = ["app/pprof/page.tsx", "components/devProfiler.tsx"]
			.flatMap((file) => rawEnglishNodes(path.join(root, file), root))
			.filter((entry) => !/(CPU %|GOMAXPROCS|GC:|Alloc \(MB\)|Heap In-Use \(MB\)|\bCPUs:\b|\bCPU:\b|\bHeap:\b)/.test(entry));
		expect(offenders).toEqual([]);
	});

	it("fully localizes provider governance and logging surfaces", () => {
		const root = process.cwd();
		const offenders = [
			"app/workspace/providers/views/modelProviderKeysTableView.tsx",
			"app/workspace/providers/fragments/governanceFormFragment.tsx",
			"app/workspace/providers/fragments/deploymentsTable.tsx",
			"app/workspace/config/views/loggingView.tsx",
			"components/ui/multibudgets.tsx",
			"components/ui/budgetUsageResetDialog.tsx",
			"components/ui/quarterStartSelect.tsx",
		].flatMap((file) => rawEnglishNodes(path.join(root, file), root));
		expect(offenders).toEqual([]);
	});

	it("fully localizes MCP authentication surfaces", () => {
		const root = process.cwd();
		const offenders = [
			"app/workspace/mcp-sessions/auth/page.tsx",
			"app/workspace/mcp-registry/views/mcpClientSheet.tsx",
			"app/workspace/mcp-registry/views/mcpClientForm.tsx",
			"app/workspace/mcp-registry/views/mcpClientsFilterSidebar.tsx",
			"app/workspace/mcp-registry/views/mcpHeadersAuthorizer.tsx",
			"app/workspace/mcp-registry/views/oauth2Authorizer.tsx",
			"app/workspace/mcp-registry/views/mcpClientsTable.tsx",
			"app/workspace/mcp-registry/views/mcpServersEmptyState.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryAddServerSheet.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryFilterSidebar.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryInstallSheet.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryServerCard.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryServersTable.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibrarySettingsSheet.tsx",
			"app/workspace/mcp-registry/library/views/mcpLibraryDeleteDialog.tsx",
		].flatMap((file) => rawEnglishNodes(path.join(root, file), root));
		expect(offenders).toEqual([]);
	});
});