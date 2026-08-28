# Go Webhook Dispatcher

[![CI](https://github.com/phelukas/go-webhook-dispatcher/actions/workflows/ci.yml/badge.svg)](https://github.com/phelukas/go-webhook-dispatcher/actions/workflows/ci.yml)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

Serviço back-end em Go para receber e entregar webhooks de forma confiável. O
projeto está sendo construído em incrementos pequenos para demonstrar decisões
de arquitetura, concorrência, idempotência, observabilidade e operação.

## Estado atual

Os dois primeiros incrementos implementam a fundação operacional e a entrada
durável de webhooks:

- servidor HTTP com `net/http`;
- configuração por variáveis de ambiente com validação;
- logs JSON estruturados com `log/slog`;
- métricas no formato Prometheus;
- endpoints de liveness e readiness;
- `POST /v1/webhooks` com validação e limite de tamanho;
- idempotência por chave, com detecção de conteúdo conflitante;
- PostgreSQL, migração versionada e teste de integração real;
- encerramento seguro com timeout;
- testes unitários, detector de corrida e CI em duas versões do Go;
- imagem Docker multi-stage executada por usuário não privilegiado.

A entrega HTTP assíncrona ainda não faz parte do projeto. O roadmap abaixo
diferencia claramente persistência concluída de processamento futuro.

## Executando

Com Docker Compose:

```bash
docker compose up --build
```

Ou com Go instalado:

```bash
go mod download
go run ./cmd/api
```

Endpoints disponíveis:

```text
GET /         informações do serviço
GET /healthz  liveness
GET /readyz   readiness
GET /metrics  métricas Prometheus
POST /v1/webhooks  submissão idempotente
```

Exemplo:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/metrics
```

Submissão:

```bash
curl -i http://localhost:8080/v1/webhooks \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-123' \
  -d '{
    "target_url": "https://example.com/hooks",
    "event_type": "order.created",
    "payload": {"order_id": "123"}
  }'
```

A primeira submissão retorna `202 Accepted`. Repetir a mesma chave e conteúdo
retorna `200 OK` com `replayed: true` e o mesmo identificador. Reutilizar a
chave com conteúdo diferente retorna `409 Conflict`.

## Configuração

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | endereço do servidor HTTP |
| `DATABASE_URL` | PostgreSQL local `dispatcher` | conexão com o banco |
| `DATABASE_TIMEOUT` | `5s` | limite para conexão e migrações |
| `READ_HEADER_TIMEOUT` | `5s` | proteção contra headers lentos |
| `SHUTDOWN_TIMEOUT` | `10s` | limite para drenar requisições |

Durações seguem o formato do pacote `time`, como `500ms`, `5s` ou `1m`.
Valores inválidos ou não positivos impedem a inicialização.

## Arquitetura atual

```text
cmd/api
   |
   v
internal/app ----------> ciclo de vida e graceful shutdown
   |
   +--> internal/config -> ambiente e validação
   |
   +--> internal/httpapi
   |       +--> health/readiness
   |       +--> POST /v1/webhooks
   |       +--> logs e métricas
   |
   +--> internal/postgres
           +--> migrações versionadas
           +--> submissões idempotentes
```

Decisões arquiteturais:

- [`ADR 0001: fundação operacional`](docs/adr/0001-service-foundation.md);
- [`ADR 0002: idempotência no PostgreSQL`](docs/adr/0002-postgres-idempotency.md).

## Qualidade

```bash
make check
```

Comandos equivalentes:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race -cover ./...
go build ./cmd/api
```

O teste PostgreSQL é executado quando `TEST_DATABASE_URL` está definida. A CI
inicia uma instância isolada do PostgreSQL 16 para esse cenário.

## Roadmap

- [x] fundação HTTP, métricas, logs e graceful shutdown;
- [x] API idempotente para submissão de webhooks;
- [x] PostgreSQL com migrações e fila durável de submissões;
- [ ] worker pool com concorrência limitada;
- [ ] retries com backoff e dead-letter queue;
- [ ] métricas de entrega e testes de integração;
- [ ] teste de carga e documentação dos resultados.

Antes da entrega externa, o worker também deverá bloquear destinos inseguros
para reduzir risco de SSRF. Apenas validar o formato da URL não é suficiente
quando o serviço começar a realizar chamadas de rede.

## Licença

Distribuído sob a [licença MIT](LICENSE).
