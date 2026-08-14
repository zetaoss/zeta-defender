.PHONY: apply delete

apply:
	kubectl apply -k . \
		--prune \
		-l app.kubernetes.io/name=zeta-defender \
		--prune-allowlist=apps/v1/Deployment \
		--prune-allowlist=core/v1/Service \
		--prune-allowlist=core/v1/ConfigMap

delete:
	kubectl delete deployments.apps,services,configmaps \
		-l app.kubernetes.io/name=zeta-defender \
		--ignore-not-found
