import { copyTextToClipboard } from "@/lib/utils/clipboard";
import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

interface UseCopyToClipboardOptions {
	successMessage?: string | false;
	errorMessage?: string | false;
	resetDelay?: number;
}

export function useCopyToClipboard(options: UseCopyToClipboardOptions = {}) {
	const { successMessage = "Copied to clipboard", errorMessage = "Failed to copy", resetDelay = 2000 } = options;
	const [copied, setCopied] = useState(false);
	const timeoutRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const mountedRef = useRef(false);

	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
			if (timeoutRef.current) clearTimeout(timeoutRef.current);
		};
	}, []);

	const copyWithResult = useCallback(
		async (text: string) => {
			try {
				await copyTextToClipboard(text);
				if (!mountedRef.current) return false;
				setCopied(true);
				if (successMessage) toast.success(successMessage);

				if (timeoutRef.current) clearTimeout(timeoutRef.current);
				timeoutRef.current = setTimeout(() => setCopied(false), resetDelay);
				return true;
			} catch {
				if (mountedRef.current && errorMessage) toast.error(errorMessage);
				return false;
			}
		},
		[successMessage, errorMessage, resetDelay],
	);
	const copy = useCallback(
		async (text: string) => {
			await copyWithResult(text);
		},
		[copyWithResult],
	);

	return { copy, copyWithResult, copied };
}