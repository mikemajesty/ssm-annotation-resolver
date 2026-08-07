# SsmAnnotationResolverInfra CRD - Guia de Uso

Este guia mostra como usar o CRD [`SsmAnnotationResolverInfra`](/Users/mike.lima/Documents/Pessoal/ssm-annotation-resolver/api/v1/ssminfra_types.go) para provisionar a infraestrutura base do SSM Annotation Resolver.

## O CRD segue boas praticas?

Sim, ele ja adota as principais praticas de CRD para operacao:

- Schema OpenAPI com campos obrigatorios e validacao (`minLength`, `enum`)
- `status` como subresource (separacao clara entre `spec` e estado observado)
- `additionalPrinterColumns` para observabilidade via `kubectl get`
- `Namespaced scope` para isolamento por ambiente/namespace
- Reconciler idempotente orientado a fase (`Pending`, `Provisioning`, `Ready`, `Failed`)

## O que ele provisiona hoje

O controller em [`infra_controller.go`](/Users/mike.lima/Documents/Pessoal/ssm-annotation-resolver/pkg/reconciler/infra_controller.go) cria:

- SQS principal
- DLQ
- IAM Role para IRSA + policy de acesso ao SQS

> Importante: a regra/target do EventBridge e configurada fora do controller (IaC/CLI), apontando para a fila criada.

## Pre-requisitos

- CRD aplicado no cluster:
  - [`config/crd/ssminfra_crd.yaml`](/Users/mike.lima/Documents/Pessoal/ssm-annotation-resolver/config/crd/ssminfra_crd.yaml)
- Controller do projeto instalado (chart):
  - [`charts/ssm-annotation-resolver/`](/Users/mike.lima/Documents/Pessoal/ssm-annotation-resolver/charts/ssm-annotation-resolver)
- Credenciais AWS/IRSA validas para criar SQS e IAM
- OIDC provider ARN do cluster EKS

## 1) Instalar o CRD

```bash
kubectl apply -f config/crd/ssminfra_crd.yaml
```

## 2) Criar uma instancia do CRD

Exemplo:

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

Aplicar:

```bash
kubectl apply -f <arquivo>.yaml
```

## 3) Acompanhar provisioning

```bash
kubectl get ssminfra -n envoy-gateway-system
kubectl describe ssminfra default -n envoy-gateway-system
```

Campos importantes de status:

- `.status.phase`
- `.status.message`
- `.status.outputs.sqsQueueUrl`
- `.status.outputs.sqsQueueArn`
- `.status.outputs.dlqQueueUrl`
- `.status.outputs.iamRoleArn`

## 4) Configurar EventBridge fora do controller

Depois do CRD ficar `Ready`, crie regra/target para enviar eventos de mudanca do SSM para a SQS provisionada.

Exemplo de regra:

```bash
aws events put-rule \
  --name ssm-parameter-store-changes \
  --event-pattern '{"source":["aws.ssm"]}'
```

Depois associe target apontando para a fila principal criada.

## 5) Consumir no Pulumi/GitOps

No fluxo Pulumi, crie o CustomResource e leia os outputs de status para alimentar o Helm chart do controller (principalmente URL da SQS e role ARN de IRSA).

## Troubleshooting rapido

- `phase=Failed`: ver `status.message` e logs do controller
- Sem reprocessamento por mudanca de parametro: validar regra/target do EventBridge
- Erro de permissao AWS: validar trust policy IRSA e policy SQS
