# Running Unio on Kubernetes

This guide covers the Helm chart in `deploy/helm/unio`: installing it, upgrading
it, backing up its credentials, and troubleshooting it. The chart runs one Unio
node per pod, with one persistent volume per drive.

The v0.1 cluster layout is fixed when the cluster is created. Read
[Scaling limits](#scaling-limits) before you install.

## What the chart creates

| Object | Purpose |
|---|---|
| StatefulSet `<release>` | One pod per node (`replicas`), started in parallel. |
| PersistentVolumeClaims `data1` to `dataN` | One per drive in each pod, mounted at `/data1` to `/dataN`. They are kept on uninstall. |
| Service `<release>-hl` (headless) | Internode traffic on port 9100. Pods are published before they are ready, so that the nodes find each other while the cluster forms. |
| Service `<release>` (ClusterIP) | S3 API and console on port 9000. |
| Secret `<release>-credentials` | Cluster secret, root user and root password, and the metrics token when enabled. The chart creates it unless you set `auth.existingSecret`. |
| PodDisruptionBudget | `maxUnavailable: 1`, so that voluntary disruptions, such as a node drain, take down one pod at a time. |
| NetworkPolicy | Internode port 9100 only from pods of the release. S3 port 9000 from any source, unless `networkPolicy.s3From` is set. Egress is not restricted. |
| Ingress (optional) | Routes to the S3 port for the API and the console. Off by default. |
| ServiceMonitor (optional) | Prometheus Operator scrape of `/unio/metrics` with the bearer token. Off by default. |

Node identity follows from the pod name. Pod `unio-0` is node `unio-0`. Its
drive endpoints use its DNS name, `unio-0.unio-hl.<namespace>.svc.<clusterDomain>`,
and the chart sets `UNIO_NODE_ID` to the same host. The release name, the
namespace and `clusterDomain` are therefore part of the cluster identity. Do not
change them for an existing cluster.

Internode traffic is always mutual TLS. Its certificates derive from the cluster
secret, so the chart needs no cert-manager and no certificate files for it.

## Prerequisites

- Kubernetes 1.27 or later. The chart was tested on Kubernetes 1.34; see
  [Verification](#verification-of-this-chart).
- Helm 4. The chart was tested with Helm 4.3.1. Helm 3 is not tested.
- A StorageClass for `ReadWriteOnce` volumes. Drive writes are synced to disk by
  default (`config.storageSync: always`), so prefer local SSD-backed storage or a
  block store with low fsync latency.
- A Unio image that every node can pull. No official image is published yet.
  Build one from the repository root and push it to a registry you control:

  ```sh
  docker build -f deploy/docker/Dockerfile -t registry.example.com/unio:0.1.0 .
  docker push registry.example.com/unio:0.1.0
  ```

- One Kubernetes node per pod when `antiAffinity: required` is set. The default,
  `preferred`, spreads the pods where it can and still schedules them when there
  are fewer nodes.
- The Prometheus Operator CRDs, only if you enable the ServiceMonitor.
- A CNI that enforces NetworkPolicy, if you rely on the policy. Without one, the
  policy is accepted and has no effect.

## Layout

The total number of drives is `replicas` times `drivesPerNode`. Unio groups the
drives into erasure sets, so the total must be 1, or have a divisor from 4 to 16.
The chart refuses any other total, such as 2 or 3 drives, when it renders the
manifests.

| replicas | drivesPerNode | drives | erasure set | parity (default) | notes |
|---|---|---|---|---|---|
| 4 | 4 | 16 | 16 | 4 | the default |
| 4 | 2 | 8 | 8 | 2 | |
| 6 | 2 | 12 | 12 | 4 | |
| 1 | 1 | 1 | 1 | 0 | development only, no redundancy |

Parity is the number of drives a set can lose without losing data. With the
default layout, one whole node (4 drives) can fail.

## Install

1. Create the credentials. The default is to let the chart create them on the
   first install, with random values. To manage them yourself, for example with
   GitOps, create a Secret with the keys `cluster-secret`, `root-user` and
   `root-password`, and `metrics-token` if you enable metrics. The values must
   meet the rules of the configuration reference: the cluster secret and the
   metrics token are at least 32 characters, and the root user is 12 to 128
   letters and digits.

   ```sh
   umask 077
   mkdir -p unio-secrets && cd unio-secrets
   openssl rand -base64 48 | tr -d '\n' > cluster-secret
   openssl rand -hex 10 | tr -d '\n' > root-user
   openssl rand -base64 36 | tr -d '\n' > root-password
   openssl rand -base64 48 | tr -d '\n' > metrics-token
   kubectl create namespace unio
   kubectl -n unio create secret generic unio-credentials \
     --from-file=cluster-secret=cluster-secret \
     --from-file=root-user=root-user \
     --from-file=root-password=root-password \
     --from-file=metrics-token=metrics-token
   cd .. && rm -r unio-secrets
   ```

   Keep a copy of the Secret, as described in [Back up the cluster secret](#back-up-the-cluster-secret).

2. Write a values file. Keep it in version control:

   ```yaml
   replicas: 4
   drivesPerNode: 4
   image:
     repository: registry.example.com/unio
     tag: 0.1.0
   persistence:
     size: 100Gi
     storageClass: fast-ssd   # omit to use the cluster default
   auth:
     existingSecret: unio-credentials   # omit to let the chart create the Secret
   ```

3. Install the chart from the repository:

   ```sh
   helm install unio deploy/helm/unio --namespace unio --create-namespace -f values.yaml
   ```

   Omit `--create-namespace` and `kubectl create namespace` if the namespace
   already exists.

4. Wait for the pods. They start together and wait for each other, so the first
   start can take a minute or more. The startup probe allows 10 minutes by
   default (`probes.startupFailureThreshold`). A pod becomes Ready when every
   Raft group it hosts has a leader.

   ```sh
   kubectl -n unio rollout status statefulset/unio
   kubectl -n unio get pods,pvc
   ```

5. Reach the S3 API. For a quick check, forward the port:

   ```sh
   kubectl -n unio port-forward svc/unio 9000:9000
   curl -i http://localhost:9000/unio/health/ready
   ```

   The root credentials are in the Secret. Read them without printing them to a
   shared terminal, and use them only from your client:

   ```sh
   kubectl -n unio get secret unio-credentials -o jsonpath='{.data.root-user}' | base64 -d
   kubectl -n unio get secret unio-credentials -o jsonpath='{.data.root-password}' | base64 -d
   ```

   Clients use region `us-east-1` (or `config.region`) and path-style addressing,
   `http://localhost:9000` in the port-forward example. Use virtual-host addressing
   only with `config.domains` and a wildcard DNS name for them.

### Expose S3 and the console

- **Ingress.** Set `ingress.s3.enabled`, `ingress.s3.className`, `ingress.s3.hosts`
  and `ingress.s3.tls`. The console is served at `/unio/console/` on the same port.
  Enable `ingress.console` to give it its own host. The Ingress examples were
  rendered and validated, but not exercised with an ingress controller in this
  release.
- **TLS on the S3 port.** Create a `kubernetes.io/tls` Secret and set
  `tls.existingSecret`:

  ```sh
  kubectl -n unio create secret tls unio-s3-tls --cert=s3.crt --key=s3.key
  ```

  The pods then serve HTTPS on 9000, and the probes switch to HTTPS. The
  certificate must cover the host names that clients use. Set `tls.require: true`
  to forbid starting the S3 endpoint without TLS.

### Metrics

Set `metrics.token.enabled: true`. `/unio/metrics` then accepts only
`Authorization: Bearer <metrics-token>`. Without a valid token it answers 403.
Set `metrics.serviceMonitor.enabled: true` to create a ServiceMonitor that sends
the token. The chart refuses a ServiceMonitor without the token.

```sh
TOKEN=$(kubectl -n unio get secret unio-credentials -o jsonpath='{.data.metrics-token}' | base64 -d)
curl -H "Authorization: Bearer $TOKEN" http://localhost:9000/unio/metrics
```

## Upgrade

```sh
helm upgrade unio deploy/helm/unio --namespace unio -f values.yaml
```

- The pods are replaced one at a time, from the highest ordinal down. Each new
  pod must be Ready before the next one restarts.
- The chart Secret keeps its values on upgrade. Helm never rotates the cluster
  secret or the root credentials. An upgrade only adds a key that is newly
  enabled, such as `metrics-token`.
- Other settings, such as the image, the Ingress, the ServiceMonitor, the
  NetworkPolicy and the values under `config`, can change on upgrade.
- Upgrades cannot change `persistence.*`, or the `replicas` and `drivesPerNode`
  layout. See [Scaling limits](#scaling-limits).

## Scaling limits

In v0.1 the cluster layout cannot change after creation. `replicas` and
`drivesPerNode` are fixed, and so are the erasure settings
(`config.erasure.*`) and the metadata partitions. The drive identities and the
erasure layout are recorded when the cluster forms.

Changing the layout of a running cluster does not work, and the failure is
safe. This was tested on a four-node release that was changed to five nodes:

- The new pod is created, but it never becomes Ready, because the existing nodes
  do not know it. The rollout stalls, and the four existing nodes keep running.
- A node that restarts with the new layout refuses to start. Its log says
  `stored cluster map differs from the configured one: node set differs ...;
  changing the cluster layout of an existing cluster is not supported in v0.1`.
- Scaling down removes the extra pods, but it keeps their PVCs. Delete those
  claims only when you are sure you no longer need them.

Changing `persistence.size` or `persistence.storageClass` on an existing release
fails, because the API server does not allow changes to the StatefulSet volume
claim templates. Growing drives in place is not supported in v0.1.

To change the layout, create a new release with the layout you need, and copy
the objects to it with an S3 client.

## Back up the cluster secret

The Secret holds values that the cluster cannot run without:

- `cluster-secret` identifies the cluster on its drives, because the cluster ID
  derives from it. It also protects the stored user secret keys. Without it, the
  drives cannot join the cluster, and the nodes crash with `cluster id differs`.
  Nothing can recover the data from the drives alone.
- `root-user` and `root-password` are the root account.
- `metrics-token` is the bearer token of the metrics endpoint.

Back up the Secret after the first install, and again after you enable a feature
that adds a key:

```sh
kubectl -n unio get secret unio-credentials -o yaml > unio-credentials.backup.yaml
```

The file holds base64 values, not encrypted ones. Store it in a password manager
or a secrets vault, and delete any plain copy.

The chart sets `helm.sh/resource-policy: keep` on its Secret. `helm uninstall`
therefore keeps the Secret. A reinstall reuses it together with the retained
PVCs, and the data stays readable.

Erasure coding protects against drive and node failures. It does not protect
against deletes, bugs, or the loss of the whole cluster. Keep an independent copy
of data that you cannot lose.

## Uninstall

```sh
helm uninstall unio --namespace unio
```

This removes the StatefulSet, the services, the PodDisruptionBudget, the
NetworkPolicy and the optional objects. It keeps the PVCs, which hold the drives,
and the credentials Secret. To destroy the cluster and its data, delete them
explicitly. This cannot be undone:

```sh
kubectl -n unio delete pvc -l app.kubernetes.io/instance=unio
kubectl -n unio delete secret unio-credentials
```

## Troubleshooting

Start with the events and the logs of the failing pod:

```sh
kubectl -n unio describe pod unio-0
kubectl -n unio logs unio-0 --previous
```

| Symptom | Cause and fix |
|---|---|
| `helm install` stops with `cannot form erasure sets` | Choose `replicas` and `drivesPerNode` so that their product is 1, or has a divisor from 4 to 16. |
| `helm` stops with `values don't meet the specifications of the schema` | A value has the wrong type or is out of range. The message names the value. |
| Pods stay Pending with unbound claims | The StorageClass does not exist, or it has no capacity. Check `kubectl get pvc`. |
| Pods stay Pending with anti-affinity messages | `antiAffinity: required` needs one Kubernetes node per pod. Use `preferred` or add nodes. |
| `CreateContainerConfigError`, `secret ... not found` or a missing key | `auth.existingSecret` does not exist, or it lacks a key. The metrics token needs `metrics-token`. |
| CrashLoopBackOff, `cluster id differs` | The cluster secret on the drives differs from the one in the Secret. The Secret was deleted or replaced. Restore the backup. |
| CrashLoopBackOff, `stored cluster map differs`, `node set differs` | The layout changed, or the release name, namespace or `clusterDomain` changed. Revert the change. See [Scaling limits](#scaling-limits). |
| Pods are not Ready, and the logs show that peers cannot be reached | Check that the pods resolve each other's names. The chart builds the names from `clusterDomain`, which must be the DNS domain of your cluster (default `cluster.local`). |
| Pods run, but are not Ready for a long time, with `raftstore: ... groups have no leader` | Expected during the first bootstrap, for up to a few minutes. If it lasts longer, check that the pods reach each other on port 9100: the headless service DNS, the NetworkPolicy and the CNI. |
| `helm upgrade` fails with `Forbidden: updates to statefulset spec` | A change to `persistence.*` or to the layout. Revert it. |
| `403 AccessDenied` on `/unio/metrics` | The bearer token is missing or wrong. Compare it with the `metrics-token` key. |
| Probes fail after you set `tls.existingSecret` | The Secret is missing, or its certificate is not valid. The probes use HTTPS, so check the pod events for the probe error. |
| Clients get `InvalidAccessKeyId` | The client uses the wrong root user, or another process answers on the local port, such as a Docker container that publishes the same port. Check what listens on the port, and which Service the port-forward targets. |

## Verification of this chart

This release was checked with these steps:

- `helm lint --strict` on Helm 4.3.1 passes.
- 31 rendering checks with Helm: valid layouts and options render, and invalid
  layouts, values and option combinations fail with the expected message.
- kubeconform 0.8.0, strict mode, with the Kubernetes 1.34.0 schemas: every
  rendered object is valid. The ServiceMonitor is skipped unless its CRD is
  installed; it was then accepted by a Kubernetes 1.34 API server.
- A kind v0.33.0 cluster with one control plane and two workers, Kubernetes
  1.34.0, and the image built from `deploy/docker/Dockerfile`:
  - all four pods became Ready in about 40 seconds, spread over the two workers;
  - S3 PUT, GET and LIST worked, including a 9 MiB multipart upload, and the
    objects stayed readable after one pod was deleted and restarted;
  - the upgrades kept the chart Secret values, enabled the metrics token,
    enabled TLS on the S3 port, and served S3 over HTTPS;
  - `existingSecret` worked with a Secret created outside the chart;
  - uninstall kept the Secret and the PVCs, and a reinstall read the old data;
  - a changed layout and a lost cluster secret each produced the errors in the
    troubleshooting table.
- NetworkPolicy: the policy was rendered and accepted, but not enforced. kind's
  default CNI does not enforce NetworkPolicy: a pod in another namespace could
  open a TCP connection to port 9100.

Not tested in this release: an ingress controller, a Prometheus scrape,
Helm 3, multi-node clusters beyond kind, drains with the PodDisruptionBudget,
`antiAffinity: required` with enough nodes, and performance.
