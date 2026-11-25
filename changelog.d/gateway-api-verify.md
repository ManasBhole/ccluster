### Added
- The ip operator verifies `gateway_api`: `Verified` stays False until the
  CustomResourceDefinitions `gateways.gateway.networking.k8s.io` and
  `httproutes.gateway.networking.k8s.io` exist and the cluster serves
  `gateway.networking.k8s.io/v1`, which cert-manager needs at startup once
  `cert-manager.config.gatewayAPI.enabled` is on. The CRDs are found by name, so Gateway
  API CRDs installed by other means pass too; ones older than Gateway API v1.0, which serve
  no v1, do not. A plane on a catalog revision from before `gateway_api` now reports it
  failing. The ip operator's ClusterRole now reads HTTPRoutes, of which it caches only
  metadata.
