interface ParsedReleaseVersion {
	major: number;
	minor: number;
	patch: number;
	prerelease: string[];
}

const RELEASE_VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/;

function parseReleaseVersion(version: string): ParsedReleaseVersion | null {
	const match = version.trim().match(RELEASE_VERSION_PATTERN);
	if (!match) return null;
	return {
		major: Number(match[1]),
		minor: Number(match[2]),
		patch: Number(match[3]),
		prerelease: match[4]?.split(".") ?? [],
	};
}

function compareCoreVersion(left: ParsedReleaseVersion, right: ParsedReleaseVersion): number {
	for (const key of ["major", "minor", "patch"] as const) {
		if (left[key] !== right[key]) return left[key] > right[key] ? 1 : -1;
	}
	return 0;
}

function comparePrerelease(left: string[], right: string[]): number {
	if (left.length === 0 && right.length === 0) return 0;
	if (left.length === 0) return 1;
	if (right.length === 0) return -1;
	for (let index = 0; index < Math.max(left.length, right.length); index++) {
		const leftPart = left[index];
		const rightPart = right[index];
		if (leftPart === undefined) return -1;
		if (rightPart === undefined) return 1;
		if (leftPart === rightPart) continue;
		const leftNumeric = /^\d+$/.test(leftPart);
		const rightNumeric = /^\d+$/.test(rightPart);
		if (leftNumeric && rightNumeric) return Number(leftPart) > Number(rightPart) ? 1 : -1;
		if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
		return leftPart > rightPart ? 1 : -1;
	}
	return 0;
}

/**
 * Update-banner policy for the Moon fork. A Moon build is treated as a
 * downstream build of its MAJOR.MINOR.PATCH base, rather than as an upstream
 * prerelease that should be prompted to install the same stable base again.
 */
export function shouldShowReleaseNotice(latestVersion: string, currentVersion: string): boolean {
	const latest = parseReleaseVersion(latestVersion);
	const current = parseReleaseVersion(currentVersion);
	if (!latest || !current) return false;
	const coreComparison = compareCoreVersion(latest, current);
	if (coreComparison !== 0) return coreComparison > 0;
	if (current.prerelease[0]?.toLowerCase() === "moon") return false;
	return comparePrerelease(latest.prerelease, current.prerelease) > 0;
}

function normalizeReleaseLabel(version: string | undefined): string {
	return (version ?? "").trim().replace(/^v/i, "").split("+")[0].toLowerCase();
}

export function isReleaseDismissed(latestVersion: string, dismissedVersion: string | undefined): boolean {
	const latest = normalizeReleaseLabel(latestVersion);
	return latest !== "" && latest === normalizeReleaseLabel(dismissedVersion);
}