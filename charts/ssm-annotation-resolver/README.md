# ssm-annotation-resolver

Helm chart for deploying the SSM Annotation Resolver webhook/controller.

## Key values

- `image.repository`: controller image
- `serviceAccount.annotations`: use this for IRSA role ARN
- `aws.region`: AWS region override
- `sqs.queueURL`: queue with Parameter Store change events
- `webhook.failurePolicy`: defaults to `Fail`
- `webhook.rules.scope`: defaults to `Namespaced` (use `*` if you need cluster-scoped resources)
- `certManager.issuerRef.*`: issuer used for webhook TLS certificate
