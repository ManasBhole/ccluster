### Added
- The ip operator verifies `cert_manager`: `Verified` stays False until every
  ValidatingWebhookConfiguration registering the webhook `webhook.cert-manager.io` has an
  injected CA bundle and a Service with a ready endpoint, the CRDs of Certificates,
  Issuers and ClusterIssuers exist, and a controller holds the Lease
  `cert-manager-controller` and has renewed it lately. A configuration left behind by an
  earlier install fails the check, because the API server calls it too. The webhook is
  found by that entry name and followed to its Service, so cert-manager passes under any
  release name or namespace.
