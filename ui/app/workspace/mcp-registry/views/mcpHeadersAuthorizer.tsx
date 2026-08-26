// MCPHeadersAuthorizer mirrors OAuth2Authorizer's UI/UX for the per-user-
// headers create flow: same Dialog shell, same wording structure, same
// state machine (confirm → input → testing → success/failed), same Cancel /
// Continue affordances. The divergence is that the verify step is a values
// form filled inline (no upstream redirect/popup), and the create call is
// a single POST /api/mcp/client where the server runs verify + discover +
// persist atomically. Mirrors per-user OAuth where the admin's temp access
// token plays the analogous role.

import HeadersForm from "@/components/headersForm";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { getErrorMessage, useCreateMCPClientMutation } from "@/lib/store";
import { CreateMCPClientRequest } from "@/lib/types/mcp";
import { Loader2 } from "lucide-react";
import React, { useEffect, useRef, useState } from "react";
import i18n from "@/lib/i18n";

interface MCPHeadersAuthorizerProps {
	open: boolean;
	onClose: () => void;
	onSuccess: () => void;
	onError: (error: string) => void;
	onConflict?: (error: string) => void;
	// Full payload the parent has already assembled. The dialog adds
	// user_headers (collected inline) and POSTs once.
	payload: CreateMCPClientRequest;
	// Required key schema, rendered as the form's input fields.
	perUserHeaderKeys: string[];
}

type Status = "confirm" | "input" | "testing" | "success" | "failed";

export const MCPHeadersAuthorizer: React.FC<MCPHeadersAuthorizerProps> = ({
	open,
	onClose,
	onSuccess,
	onError,
	onConflict,
	payload,
	perUserHeaderKeys,
}) => {
	const [status, setStatus] = useState<Status>("confirm");
	const [errorMessage, setErrorMessage] = useState<string | null>(null);
	// Set to true when the user cancels so in-flight async callbacks do not
	// invoke onSuccess / onError / onClose after the dialog is dismissed.
	const cancelledRef = useRef(false);

	const [createMCPClient] = useCreateMCPClientMutation();

	// Reset state every time the dialog opens so a retry from a previous
	// session doesn't carry over.
	useEffect(() => {
		if (open) {
			setStatus("confirm");
			setErrorMessage(null);
			cancelledRef.current = false;
		}
	}, [open]);

	const handleConfirm = () => {
		setStatus("input");
	};

	const handleRunTest = async (values: Record<string, string>) => {
		if (cancelledRef.current) return;
		setStatus("testing");
		try {
			await createMCPClient({ ...payload, user_headers: values }).unwrap();
			if (cancelledRef.current) return;
			setStatus("success");
			onSuccess();
		} catch (err) {
			if (cancelledRef.current) return;
			const errMsg = getErrorMessage(err);
			if ((err as any)?.status === 409) {
				setStatus("input");
				setErrorMessage(null);
				onConflict?.(errMsg);
				return;
			}
			setStatus("failed");
			setErrorMessage(errMsg);
			onError(errMsg);
		}
	};

	const handleRetry = () => {
		setErrorMessage(null);
		setStatus("input");
	};

	const handleCancel = () => {
		cancelledRef.current = true;
		onClose();
	};

	return (
		<Dialog
			open={open}
			onOpenChange={(nextOpen) => {
				if (!nextOpen) {
					handleCancel();
				}
			}}
		>
			<DialogContent
				className="sm:max-w-md"
				onPointerDownOutside={(e) => {
					e.preventDefault();
					handleCancel();
				}}
				onEscapeKeyDown={(e) => {
					e.preventDefault();
					handleCancel();
				}}
			>
				<DialogHeader>
					<DialogTitle>
						{status === "confirm" ? i18n.t("workspace.mcpForm.testHeaderConfiguration") : i18n.t("workspace.mcpForm.headerAuthorization")}
					</DialogTitle>
					<DialogDescription>
						{status === "confirm" && i18n.t("workspace.mcpForm.headerTestNeeded")}
						{status === "input" && i18n.t("workspace.mcpForm.enterSampleValues")}
						{status === "testing" && i18n.t("workspace.mcpForm.verifyingConnection")}
						{status === "success" && i18n.t("workspace.mcpForm.verificationSuccessful")}
						{status === "failed" && i18n.t("workspace.mcpForm.verificationFailed")}
					</DialogDescription>
				</DialogHeader>

				<div className="flex flex-col space-y-4">
					{status === "confirm" && (
						<>
							<div className="text-muted-foreground space-y-3 text-sm">
								<p>{i18n.t("workspace.mcpForm.headerSetupDescription")}</p>
								<p>{i18n.t("workspace.mcpForm.headerOneTimeTest")}</p>
								<p>{i18n.t("workspace.mcpForm.eachUserSubmitsHeaders")}</p>
							</div>
							<div className="flex w-full justify-end space-x-2">
								<Button onClick={handleCancel} variant="outline" data-testid="per-user-headers-cancel">
									{i18n.t("common.cancel")}
								</Button>
								<Button onClick={handleConfirm} data-testid="per-user-headers-confirm">
									{i18n.t("workspace.mcpForm.continueWithTest")}
								</Button>
							</div>
						</>
					)}

					{status === "input" && (
						<>
							<p className="text-muted-foreground text-sm">{i18n.t("workspace.mcpForm.verificationValuesNotPersisted")}</p>
							<HeadersForm
								requiredKeys={perUserHeaderKeys}
								onSubmit={handleRunTest}
								submitLabel={i18n.t("workspace.mcpForm.runTest")}
								onCancel={handleCancel}
								testIdPrefix="per-user-headers-admin-test"
							/>
						</>
					)}

					{status === "testing" && (
						<>
							<div className="flex flex-col items-center space-y-2">
								<Loader2 className="text-secondary-foreground h-4 w-4 animate-spin" />
								<p className="text-muted-foreground text-sm">{i18n.t("workspace.mcpForm.verifyingAndDiscovering")}</p>
							</div>
						</>
					)}

					{status === "success" && (
						<div className="flex flex-col items-center space-y-2">
							<div className="flex h-12 w-12 items-center justify-center rounded-full bg-green-100">
								<svg className="h-6 w-6 text-green-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
									<path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
								</svg>
							</div>
							<p className="text-sm text-green-600">{i18n.t("workspace.mcpForm.serverConnectedSuccessfully")}</p>
						</div>
					)}

					{status === "failed" && (
						<div className="flex flex-col items-center space-y-2">
							<div className="flex h-12 w-12 items-center justify-center rounded-full bg-red-100">
								<svg className="h-6 w-6 text-red-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
									<path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
								</svg>
							</div>
							<p className="text-sm text-red-600">{errorMessage || "An error occurred"}</p>
							<Button onClick={handleRetry} variant="outline" data-testid="mcp-headers-authorizer-retry-btn">
								{i18n.t("workspace.oauth.retry")}
							</Button>
						</div>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
};