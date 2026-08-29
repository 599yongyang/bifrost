import ContactUsView from "../../views/contactUsView";
import i18n from "@/lib/i18n";

interface KafkaConnectorViewProps {
	onDelete?: () => void;
	isDeleting?: boolean;
}

export default function KafkaConnectorView(_props: KafkaConnectorViewProps) {
	return (
		<div className="space-y-6">
			<div className="space-y-4">
				<div className="flex w-full flex-col items-center justify-center py-8">
					<ContactUsView
						align="middle"
						className="mx-auto w-full max-w-lg"
						testIdPrefix="kafka-connector"
						icon={<img src="/images/kafka-logo.svg" alt="Kafka" width={88} height={88} />}
						title={i18n.t("workspace.enterpriseFallbacks.kafkaTitle")}
						description={i18n.t("workspace.enterpriseFallbacks.kafkaDescription")}
						readmeLink="https://docs.getbifrost.ai/enterprise/kafka-connector"
					/>
				</div>
			</div>
		</div>
	);
}