# Kubernetes deployment

This deployment runs a single Piper server with an in-process Kubernetes
runtime. Pipeline Jobs, Notebook StatefulSets, and Serving Deployments are all
created, reconciled, recovered, and canceled directly through the in-cluster
Kubernetes API — there is no worker tunnel and no separate worker process.

## Install

The published image is `ghcr.io/loykin/piper:latest`. For a local image, update
the image name in `server.yaml` or load the matching `piper/piper:latest`
image and set `imagePullPolicy: IfNotPresent`. Pin the image reference to an
immutable `sha-<commit>` tag for a production rollout.

```bash
./deploy/k8s/install.sh
kubectl -n piper port-forward service/piper-server 8080:8080
```

Open <http://localhost:8080>. The first browser visit guides you through creating
the initial administrator.

The installer creates `piper-server-secrets` once and reuses it on later runs.
It contains the authentication signing key, credential-encryption key, and the
`workload_token` shared secret that guards Piper's built-in `/store` HTTP
endpoint (used when Kubernetes pods fetch or write artifacts through Piper's
own file-backed artifact store rather than an external S3-compatible one).
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

Edit `piper.yaml` for non-secret server and runtime settings. Kustomize hashes the
generated ConfigMap name, so changing that file triggers a Deployment rollout.
