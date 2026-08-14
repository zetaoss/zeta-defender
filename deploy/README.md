# Kubernetes deployment

This deployment runs one active zeta-defender instance. Its `Recreate` strategy
prevents old and new Pods from overlapping during an update.

Before deploying, update the Prometheus endpoint, expression, and Cloudflare
zone ID in [`config.yaml`](config.yaml), and replace the image reference in
[`deployment.yaml`](deployment.yaml) with the image you published.

Create the Cloudflare API token Secret without storing the token in this
repository:

```sh
kubectl create secret generic zeta-defender \
  --from-literal=cloudflare-api-token='replace-me' \
  --dry-run=client -o yaml | kubectl apply -f -
```

Then apply the manifests from the repository root:

```sh
make -C deploy apply
```

The apply target prunes obsolete Deployments, Services, and generated
ConfigMaps with the `app.kubernetes.io/name=zeta-defender` label. Other resource
kinds and unlabeled resources, including the API token Secret, are not pruned.

Delete the deployed resources while retaining the API token Secret with:

```sh
make -C deploy delete
```
