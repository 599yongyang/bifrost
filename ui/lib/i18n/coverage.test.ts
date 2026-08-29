import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const visibleAttributes = new Set(["placeholder", "title", "aria-label", "alt"]);
const properNames = new Set(["Bifrost", "Python", "TypeScript", "OpenAI SDK", "Anthropic SDK", "Google GenAI SDK", "LiteLLM SDK", "LangChain SDK"]);
// Ratchet from the first v2-wide scan. New UI must not increase this while
// feature-focused tests below drive high-priority surfaces to zero.
const rawEnglishBaseline = 3803;

function tsxFiles(directory: string): string[] {
	return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
		if (entry.isDirectory() && ["node_modules", "out", ".git"].includes(entry.name)) return [];
		const fullPath = path.join(directory, entry.name);
		if (entry.isDirectory()) return tsxFiles(fullPath);
		return entry.isFile() && entry.name.endsWith(".tsx") ? [fullPath] : [];
	});
}

function looksUserFacing(value: string): boolean {
	const text = value.replace(/\s+/g, " ").trim();
	if (!/[A-Za-z]{2}/.test(text) || properNames.has(text)) return false;
	if (/^(true|false|ms|s \/ min)$/i.test(text) || /^&[a-z]+;$/.test(text)) return false;
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
			if (/^(toast\.(success|error|info|warning)|confirm|alert)$/.test(node.expression.getText(source))) note(node.arguments[0], node.arguments[0].text);
		}
		ts.forEachChild(node, visit);
	};
	visit(source);
	return found;
}

describe("i18n component coverage", () => {
	it("does not add unlocalized user-facing English", () => {
		const root = process.cwd();
		const offenders = [...tsxFiles(path.join(root, "app")), ...tsxFiles(path.join(root, "components"))].flatMap((file) =>
			rawEnglishNodes(file, root),
		);
		const counts = new Map<string, number>();
		for (const offender of offenders) {
			const file = offender.slice(0, offender.indexOf(":"));
			counts.set(file, (counts.get(file) ?? 0) + 1);
		}
		const hotspots = [...counts.entries()]
			.sort((a, b) => b[1] - a[1])
			.slice(0, 20)
			.map(([file, count]) => `${count}\t${file}`)
			.join("\n");
		expect(offenders.length, `${hotspots}\n\n${offenders.slice(0, 40).join("\n")}`).toBeLessThanOrEqual(rawEnglishBaseline);
	});

	it("fully localizes routing-rule surfaces", () => {
		const root = process.cwd();
		const sharedBuilderFiles = [
			"components/ui/custom/celBuilder/celRuleBuilder.tsx",
			"components/ui/custom/celBuilder/fieldSelector.tsx",
			"components/ui/custom/celBuilder/operatorSelector.tsx",
			"components/ui/custom/celBuilder/valueEditor.tsx",
		];
		const offenders = [
			...tsxFiles(path.join(root, "app/workspace/routing-rules")),
			...sharedBuilderFiles.map((file) => path.join(root, file)),
		].flatMap((file) => rawEnglishNodes(file, root));
		expect(offenders, offenders.join("\n")).toEqual([]);
	});

	it("does not increase remaining observability English while migration continues", () => {
		const root = process.cwd();
		const offenders = [
			...tsxFiles(path.join(root, "app/workspace/observability")),
			...tsxFiles(path.join(root, "app/workspace/alerting")),
		].flatMap((file) => rawEnglishNodes(file, root));
		expect(offenders.length, offenders.join("\n")).toBeLessThanOrEqual(146);
	});
});
