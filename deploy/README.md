# Kubernetes deployment

This deployment runs one active zeta-defender instance. Its `Recreate` strategy
prevents old and new Pods from overlapping during an update.

Before deploying, update the Prometheus endpoint, expression, and Cloudflare
zone ID in [`configmap.yaml`](configmap.yaml), and replace the image reference in
[`deployment.yaml`](deployment.yaml) with the image you published.

Create the Cloudflare API token Secret without storing the token in this
repository:

```sh
kubectl create secret generic zeta-defender \
  --from-literal=cloudflare-api-token='replace-me' \
  --dry-run=client -o yaml | kubectl apply -f -
```

Then apply the manifests:

```sh
kubectl apply -k deploy
```
