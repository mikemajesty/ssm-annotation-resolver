# SSM Annotation Resolver

Mutating admission webhook + controller that resolves Kubernetes annotation
values in the format `#:ssm:/path/to/parameter` using AWS SSM Parameter Store.

## Context

This project exists to solve a GitOps/runtime gap: some infrastructure values are
only known in AWS at runtime (for example, values produced by other stacks), but
Kubernetes manifests still need those values before persistence.

Instead of hardcoding sensitive/dynamic values in Git, resources can use
`#:ssm:/path/to/parameter` placeholders in annotations. The webhook resolves them
at admission time, and the controller re-triggers resources when the source SSM
parameter changes.

## How it works

1. Argo CD (or kubectl) applies a resource with a placeholder annotation.
2. The webhook intercepts CREATE/UPDATE, fetches SSM values, and patches the
   resource before persistence.
3. The controller:
   - retries unresolved placeholders found in the cluster on startup;
   - consumes EventBridge -> SQS events for SSM changes and re-triggers affected
     resources.

No in-memory cache is used; values are resolved directly from SSM.

## Project layout

- `main.go`: app bootstrap (manager, webhook, runner).
- `pkg/resolver/`: webhook + SSM resolver.
- `pkg/reconciler/`: SQS-driven reconciliation loop.
- `charts/ssm-annotation-resolver/`: Helm chart.

## Required AWS permissions (IRSA role)

- `ssm:GetParameter`
- `ssm:GetParameters`
- `sqs:ReceiveMessage`
- `sqs:DeleteMessage`
- `sqs:GetQueueAttributes`

## Local validation

```bash
go test ./...
make rbac
./bin/helm template ssm-annotation-resolver ./charts/ssm-annotation-resolver
```

## Release flow

- Build/push image with `make push`
- Package/push chart with `make chart`

Default registries are defined in `Makefile`.

## Integration with Foundation/GitOps

### Using the SsmAnnotationResolverInfra CRD (Recommended)

The **SsmAnnotationResolverInfra CRD** is the declarative way to provision AWS infrastructure (SQS, EventBridge, IAM) directly from Kubernetes.

**Flow:**
1. Pulumi creates a `SsmAnnotationResolverInfra` custom resource in the cluster
2. The SSM Annotation Resolver controller observes the CRD
3. Controller automatically provisions SQS queue, DLQ, EventBridge rule, and IAM role
4. Controller updates the CRD status with outputs (sqsQueueUrl, iamRoleArn, etc.)
5. Pulumi reads the status and uses outputs for Helm chart or other deployments

**Example CRD:**
```yaml
apiVersion: ssm-annotation-resolver.io/v1
kind: SsmAnnotationResolverInfra
metadata:
  name: default
  namespace: envoy-gateway-system
spec:
  sqsQueueName: ssm-annotation-resolver-queue
  dlqQueueName: ssm-annotation-resolver-dlq
  eventBridgeRuleName: ssm-parameter-store-changes
  iamRoleName: ssm-annotation-resolver-role
  awsRegion: us-east-1
  oidcProviderArn: arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLEID
  serviceAccountName: ssm-annotation-resolver
```

**Monitor provisioning:**
```bash
kubectl get ssminfra -n envoy-gateway-system -o wide
kubectl describe ssminfra default -n envoy-gateway-system
```

**Advantages:**
- ✅ GitOps native (CRD in git, declarative)
- ✅ Durability (CRD in etcd, survives failures)
- ✅ Observable (kubectl commands, status updates)
- ✅ Self-healing (controller retries on failure)
- ✅ Encapsulation (no need to understand SQS/IAM details)

### Architecture Flow

```
Foundation (Pulumi)
  ├─ Creates: SSM Parameter (e.g., /boilerplate/dev/infra/envoy-nlb-sg-id)
  ├─ Creates: SsmAnnotationResolverInfra CRD (in cluster)
  └─ Waits for: CRD status.phase == "Ready"

SsmAnnotationResolverInfra Controller (in-cluster)
  ├─ Observes CRD creation
  ├─ Provisions: SQS Queue + DLQ
  ├─ Provisions: EventBridge Rule
  ├─ Provisions: IAM Role (IRSA)
  └─ Updates: CRD status with outputs (sqsQueueUrl, iamRoleArn, etc.)

Pulumi continues
  ├─ Reads: CRD status outputs
  ├─ Deploys: Helm chart with SQS queue URL
  └─ Exports: Outputs for other stacks

GitOps Kubernetes Manifests
  ├─ Reference SSM Parameters in annotations
  │  (e.g., annotation-key: #:ssm:/path/to/parameter)
  └─ Deployed to cluster via Argo CD

SSM Annotation Resolver Webhook (in-cluster)
  ├─ Intercepts resource creation/update
  ├─ Resolves #:ssm:... placeholders from SSM Parameter Store (real-time)
  ├─ Patches resource with resolved values before persistence
  └─ Tracks sources for event-driven re-resolution

SSM Annotation Resolver Reconciler (in-cluster)
  ├─ Consumes EventBridge -> SQS events for parameter changes
  ├─ Re-resolves affected resources automatically
  └─ Handles DLQ for failed messages
```

### Example: Private Origin Envoy Proxy

**Foundation step 1: Create SSM parameter for NLB security group**
```typescript
// IaC/foundation/src/network/network-nlb-parameter-store.ts
// Writes to SSM: /boilerplate/dev/infra/envoy-nlb-sg-id = sg-xxxxx
```

**Foundation step 2: Create infrastructure via CRD**
```typescript
// IaC/foundation/index.ts (using Pulumi Kubernetes provider)
const infraConfig = new k8s.apiextensions.CustomResource('ssm-resolver-infra', {
  apiVersion: 'ssm-annotation-resolver.io/v1',
  kind: 'SsmAnnotationResolverInfra',
  metadata: {
    name: 'default',
    namespace: 'envoy-gateway-system'
  },
  spec: {
    sqsQueueName: 'ssm-annotation-resolver-queue',
    dlqQueueName: 'ssm-annotation-resolver-dlq',
    eventBridgeRuleName: 'ssm-parameter-store-changes',
    iamRoleName: 'ssm-annotation-resolver-role',
    awsRegion: 'us-east-1',
    oidcProviderArn: eksOidcProvider.oidcProviderArn
  }
}, { provider: workloadK8sProvider })

// Pulumi waits for CRD to be ready, then reads outputs
const sqsQueueUrl = infraConfig.status.outputs.sqsQueueUrl
const iamRoleArn = infraConfig.status.outputs.iamRoleArn

// Deploy Helm chart with outputs
const chart = new k8s.helm.v3.Chart('ssm-resolver', {
  chart: 'ssm-annotation-resolver',
  values: {
    sqs: {
      queueURL: sqsQueueUrl
    },
    irsa: {
      enabled: true,
      roleArn: iamRoleArn
    }
  }
})
```

**GitOps step 3: Deploy resource with placeholders**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: private-origin-envoy
  namespace: envoy-gateway-system
  annotations:
    ssm-annotation-resolver.io/enabled: "true"
    # Webhook resolves #:ssm: to value from SSM Parameter Store
    envoy-nlb-sg-id: "#:ssm:/boilerplate/dev/infra/envoy-nlb-sg-id"
data:
  config.yaml: |
    upstream:
      securityGroupId: value-will-be-resolved-from-ssm
```

**When SSM parameter changes:**
1. EventBridge detects SSM change
2. Sends event to SQS queue
3. Reconciler receives SQS message
4. Updates resource with new SSM value

## Installation
1. Foundation updates SSM parameter `/boilerplate/dev/infra/envoy-nlb-sg-id` → new value `sg-67890`
2. EventBridge publishes event to SQS queue
3. ssm-annotation-resolver reconciler receives event, extracts parameter path
4. Reconciler finds all resources with this parameter in their `sources` annotation
5. Patches resource with `ssm-annotation-resolver.io/reconcile-at: <timestamp>`
6. Kubernetes detects resource change, triggers webhook again
7. Webhook re-resolves parameter → calls SSM GetParameter → retrieves updated `sg-67890`
8. Resource updated to new value ✅

## Operational runbook

### Expected SSM change event shape (via EventBridge -> SQS)

The reconciler reads the parameter path from one of these fields:

- `detail.name`
- `detail.requestParameters.name`

Example payloads:

```json
{
  "source": "aws.ssm",
  "detail-type": "Parameter Store Change",
  "detail": {
    "name": "/my/app/config/value"
  }
}
```

```json
{
  "detail": {
    "requestParameters": {
      "name": "/my/app/config/value"
    }
  }
}
```

### Queue and DLQ recommendations

- Configure `sqs.queueURL` in chart values.
- Use a DLQ with `maxReceiveCount` > 1 to retain poison messages.
- Enable CloudWatch alarms for:
  - messages visible in the main queue above expected baseline;
  - messages visible in the DLQ (> 0 should alert).
- Keep EventBridge rule scope limited to Parameter Store change events only.

### Troubleshooting unresolved placeholders

1. Confirm the resource still contains placeholder annotations (`#:ssm:/...`).
2. Confirm controller logs for:
   - `failed to resolve SSM parameter for annotation` (webhook path);
   - `unable to parse parameter path from SQS message` (event shape issue).
3. Validate IAM/IRSA permissions:
   - `ssm:GetParameter`, `ssm:GetParameters`,
   - `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:GetQueueAttributes`.
4. Check the SSM parameter exists in the configured region (`aws.region` / `AWS_REGION`).
5. Validate webhook health and TLS/cert-manager resources.
6. If event-driven refresh is not happening, inspect SQS message body format and DLQ.

## Suggested improvements

- Add cluster-level e2e validation (envtest or real cluster) to assert webhook
  registration, admission patches, and reconcile touch behavior end-to-end.
- Review webhook scope (`apiGroups/resources = '*'`) and narrow it where possible
  to reduce blast radius and unnecessary admission traffic.
- Re-evaluate `webhook.failurePolicy` (`Fail` by default in chart) based on
  environment criticality and failure tolerance.