# Kubernetes deployment

This deployment runs one Piper control plane and one outbound-tunnel Kubernetes
worker. The server and worker communicate through the single HTTP/gRPC endpoint
on port 8080. Both processes read the generated ConfigMap from `piper.yaml`;
their commands contain no operational flags.

## Install

The published image is `ghcr.io/loykin/piper:latest`. For a local image, update
the image names in `server.yaml` and `k8s-worker.yaml` or load the matching
`piper/piper:latest` image and set `imagePullPolicy: IfNotPresent`.
Pin all three image references to the same immutable `sha-<commit>` tag for a
production rollout.

```bash
./deploy/k8s/install.sh
kubectl -n piper port-forward service/piper-server 8080:8080
```

Open <http://localhost:8080>. The first browser visit guides you through creating
the initial administrator.

The installer creates `piper-server-secrets` once and reuses it on later runs.
It contains the authentication signing key, credential-encryption key, and the
shared worker-tunnel token.
Back up that Secret and the `piper-server-data` PVC together. Losing or rotating
the authentication key invalidates sessions; losing the encryption key makes
stored credentials unreadable.

To manage secrets yourself, create `piper-server-secrets` with the three keys shown
in `server-secret.example.yaml`, then run:

```bash
kubectl apply -k deploy/k8s
```

The base deployment uses Piper's built-in file artifact store. For shared
production workloads, configure an S3-compatible store using
`storage-secret.example.yaml`, or deploy the optional `seaweedfs.yaml`.

The included Service is cluster-internal. Expose it through your ingress or load
balancer with TLS in production. SQLite and the included PVC intentionally use
one server replica; use PostgreSQL and an appropriate availability design before
scaling the control plane.

Edit `piper.yaml` for non-secret server and worker settings. Kustomize hashes the
generated ConfigMap name, so changing that file triggers a Deployment rollout.
