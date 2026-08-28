# Go Webhook Dispatcher

[![CI](https://github.com/phelukas/go-webhook-dispatcher/actions/workflows/ci.yml/badge.svg)](https://github.com/phelukas/go-webhook-dispatcher/actions/workflows/ci.yml)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

Serviço back-end em Go para receber e entregar webhooks de forma confiável. O
projeto está sendo construído em incrementos pequenos para demonstrar decisões
de arquitetura, concorrência, idempotência, observabilidade e operação.

## Estado atual

O primeiro incremento implementa a fundação operacional do serviço:

- servidor HTTP com `net/http`;
- configuração por variáveis de ambiente com validação;
- logs JSON estruturados com `log/slog`;
- métricas no formato Prometheus;
- endpoints de liveness e readiness;
- encerramento seguro com timeout;
- testes unitários, detector de corrida e CI em duas versões do Go;
- imagem Docker multi-stage executada por usuário não privilegiado.

A submissão e a entrega de webhooks ainda não fazem parte deste incremento. O
roadmap abaixo diferencia claramente o que está pronto do que será construído.

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
```

Exemplo:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/metrics
```

## Configuração

| Variável | Padrão | Finalidade |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | endereço do servidor HTTP |
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
           +--> health/readiness
           +--> logs estruturados
           +--> métricas Prometheus
```

A decisão de começar pela base operacional está registrada em
[`docs/adr/0001-service-foundation.md`](docs/adr/0001-service-foundation.md).

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

## Roadmap

- [x] fundação HTTP, métricas, logs e graceful shutdown;
- [ ] API idempotente para submissão de webhooks;
- [ ] PostgreSQL com migrações e padrão outbox;
- [ ] worker pool com concorrência limitada;
- [ ] retries com backoff e dead-letter queue;
- [ ] métricas de entrega e testes de integração;
- [ ] teste de carga e documentação dos resultados.

## Licença

Distribuído sob a [licença MIT](LICENSE).
