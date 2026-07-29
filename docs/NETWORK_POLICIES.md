# Network Policies

The `multiclusterhub-operator` deploys a set of [`NetworkPolicy`](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
resources for the `open-cluster-management-backup` namespace as part of the cluster-backup Helm
chart (`pkg/templates/charts/toggle/cluster-backup/`), controlled by `global.networkPolicies.enabled`.
Unlike a single-component policy, this is a **deny-by-default** posture for the whole namespace:
a `deny-all` policy blocks all ingress and egress for every pod, and a small set of explicit
allow rules re-opens only the traffic each component actually needs. The policies are created
alongside the other cluster-backup resources and are removed if the toggle is disabled.

Introduced in [multiclusterhub-operator#4431](https://github.com/stolostron/multiclusterhub-operator/pull/4431)
([ACM-37736](https://issues.redhat.com/browse/ACM-37736), [ACM-31154](https://issues.redhat.com/browse/ACM-31154)).

## Design principles

- **Namespace-scoped deny-all, not per-pod.** Because `open-cluster-management-backup` is
  dedicated to backup/restore components only (unlike `open-cluster-management`, which hosts many
  unrelated ACM components), a namespace-wide `deny-all` is safe here and gives the strongest
  default posture. Each allow rule then targets a specific pod selector rather than the whole
  namespace, so opening one flow doesn't accidentally open others.
- **Well-known namespace labels.** Rules that reference OpenShift system namespaces use
  `kubernetes.io/metadata.name` (stamped automatically on every namespace, Kubernetes 1.21+) or
  the OpenShift-managed `network.openshift.io/policy-group` / `policy-group.network.openshift.io/host-network`
  labels, rather than custom labels that may not exist in every cluster.
- **Egress is restricted where it's safe to predict, left open where it isn't.** DNS egress is
  locked down to a specific namespace and port set. API server egress is restricted by **port
  only** (443/6443), with no destination selector — see
  [`allow-apiserver-egress` has no destination selector](#allow-apiserver-egress-has-no-destination-selector)
  below. Object storage egress (Velero, node-agent) is left fully unrestricted — see
  [Unrestricted egress for Velero/node-agent](#unrestricted-egress-for-veleronode-agent) below.

## Component network flows

| Pod | Ingress Allowed | Egress Allowed |
|---|---|---|
| cluster-backup-operator (webhook) | Port 9443, from the API server (host-network) | DNS (53/5353) + API server (443/6443) |
| OADP controller-manager | Metrics (port 8443), from `openshift-monitoring` | DNS (53/5353) + API server (443/6443) |
| velero | Metrics (port 8085), from `openshift-monitoring` | **Unrestricted** (needs S3/MinIO/Ceph/etc.) |
| node-agent | *(none)* | **Unrestricted** (needs object storage for backup data) |
| *(all pods, baseline)* | *(none, unless opened above)* | DNS (53/5353) + API server (443/6443) |

### Policy manifests

| Policy | Selector | Direction | Rule |
|---|---|---|---|
| `deny-all` | all pods (`{}`) | Ingress + Egress | No rules — baseline deny. |
| `allow-dns-egress` | all pods (`{}`) | Egress | To `openshift-dns` namespace, ports 53 + 5353 (UDP/TCP). |
| `allow-apiserver-egress` | all pods (`{}`) | Egress | Port 443 + 6443, **no destination selector** — allows any destination on these ports, not only the API server. |
| `allow-storage-egress` | `app.kubernetes.io/name in (velero, node-agent)` | Egress | `{}` — fully unrestricted. |
| `allow-webhook-ingress` | `app: cluster-backup-chart, component: clusterbackup` | Ingress | Port 9443, from namespaces labeled `policy-group.network.openshift.io/host-network` (the API server runs on host network). |
| `allow-velero-metrics-ingress` | `app.kubernetes.io/name: velero` | Ingress | Port 8085, from namespaces labeled `network.openshift.io/policy-group: monitoring`. |
| `allow-oadp-metrics-ingress` | `control-plane: controller-manager` | Ingress | Port 8443, from namespaces labeled `network.openshift.io/policy-group: monitoring`. |

## Design decisions

### Unrestricted egress for Velero/node-agent

`allow-storage-egress` allows **all** egress for `velero` and `node-agent` pods, with no port
restriction. This was chosen over locking down to port 443 because:

1. **DR is a critical safety system** — if network policies silently break backups on a
   customer's MinIO-on-9000 setup, the failure only surfaces during a disaster, when it's too
   late to fix.
2. **The pod selector is still restrictive** — only `velero` and `node-agent` pods get broad
   egress, not the backup operator or the OADP controller.
3. **Customer environments are diverse** — MinIO (9000), Ceph RGW (8080), or custom ports
   behind load balancers are all common, especially in air-gapped/on-prem environments.
4. **The BSL endpoint is customer-configured** — the port a given `BackupStorageLocation` uses
   can't be predicted in advance.

### `allow-apiserver-egress` has no destination selector

The rule only restricts by **port** (443, 6443), not by destination — there's no `to:` selector,
so it opens those two ports to any destination for every pod in the namespace, not just the API
server. The flow table above describes this rule by its intended purpose ("API server egress"),
but it's worth being explicit that the policy itself is broader than that name suggests.

### DNS port 5353

The DNS egress rule includes port 5353 in addition to port 53. This is required because
OVN-Kubernetes (the default CNI on OpenShift) evaluates NetworkPolicy **post-DNAT**, and CoreDNS
pods listen on port 5353 while the `dns-default` Service exposes port 53. Without the 5353 rule,
DNS resolution fails silently once the policy is enabled.

### Webhook ingress via host-network label

The cluster-backup-operator's validating webhook is called by the Kubernetes API server, which
runs on the node's host network rather than inside a normal pod. `allow-webhook-ingress`
therefore matches on the OpenShift-managed `policy-group.network.openshift.io/host-network` label
instead of a specific namespace, since there's no single "API server namespace" to select.

### OADP metrics ingress may be a no-op today

`allow-oadp-metrics-ingress` opens port 8443 for OADP's controller-manager, but as of OADP 1.5.x
the OADP controller-manager doesn't expose a metrics endpoint (the Service has no backing
endpoints). The rule is in place for forward compatibility with future OADP versions that do
expose one — see [Testing performed](#testing-performed).

## Testing performed

Verified on an OpenShift 4.21 cluster with ACM 2.17 + OADP 1.5.7:

| # | Test | Result |
|---|---|---|
| 1 | Backup operator reaches API server (leader election, CRD watches, reconciliation) | PASS |
| 2 | Webhook accessible (API server → port 9443, validates Restore CRs) | PASS |
| 3 | DNS resolution for all pods (internal + external hostnames) | PASS |
| 4 | Velero reaches S3 endpoint (confirmed via `InvalidAccessKeyId` error, i.e. network works) | PASS |
| 5 | Prometheus scrapes Velero metrics from `openshift-monitoring` (port 8085) | PASS |
| 6 | BackupSchedule reconciliation (detects BSL unavailable, sets `FailedValidation`) | PASS |
| 7 | Restore reconciliation (detects active schedule conflict) | PASS |
| 8 | **Negative:** Velero metrics blocked from a non-monitoring namespace | PASS (timeout) |
| 9 | **Negative:** Operator metrics (port 8080) blocked from a non-monitoring namespace | PASS (timeout) |

**Not testable at the time:** OADP metrics (port 8443) — OADP 1.5.x exposes no metrics endpoint
to test against (see [OADP metrics ingress may be a no-op today](#oadp-metrics-ingress-may-be-a-no-op-today)).

Because these policies are enforced by the cluster's CNI plugin (not the Kubernetes API server),
any future changes to this policy set should be re-verified against a real cluster with a
NetworkPolicy-enforcing CNI (e.g. OVN-Kubernetes on OpenShift) — a `kubectl apply --dry-run`
cannot catch a rule that silently blocks legitimate traffic.
