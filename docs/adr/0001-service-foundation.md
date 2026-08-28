# ADR 0001: Base operacional antes do processamento de webhooks

- Status: aceito
- Data: 2026-08-28

## Contexto

O serviço terá processamento concorrente, persistência e chamadas HTTP externas.
Essas capacidades aumentam o custo de diagnosticar falhas se observabilidade e
encerramento seguro forem adicionados somente no final.

## Decisão

O primeiro incremento entrega apenas a fundação operacional:

- `net/http` da biblioteca padrão para reduzir abstrações prematuras;
- configuração validada a partir do ambiente;
- logs estruturados com `log/slog`;
- métricas Prometheus com labels de cardinalidade controlada;
- endpoints separados de liveness e readiness;
- `graceful shutdown` com timeout explícito;
- testes com detector de corrida na CI.

## Consequências

O projeto ainda não entrega webhooks neste incremento. Em contrapartida, os
próximos componentes — PostgreSQL, API de submissão e workers — entram sobre um
processo observável, testável e capaz de drenar requisições em andamento.

O uso de rotas fixas nas métricas é deliberado: URLs arbitrárias não viram
labels e não geram cardinalidade ilimitada no Prometheus.
