# ADR 0002: Idempotência persistida no PostgreSQL

- Status: aceito
- Data: 2026-08-28

## Contexto

Clientes podem repetir uma submissão quando perdem a resposta ou encontram uma
falha temporária. Criar uma nova entrega a cada tentativa geraria webhooks
duplicados e transferiria o problema para o consumidor.

## Decisão

Cada submissão exige o header `Idempotency-Key`. O PostgreSQL mantém uma
restrição `UNIQUE` nessa chave e o repositório segue três resultados:

- chave nova: persiste o webhook e retorna `202 Accepted`;
- chave existente com URL, evento e payload JSONB equivalentes: retorna o mesmo
  recurso com `200 OK`;
- chave existente com conteúdo diferente: retorna `409 Conflict`.

O insert usa `ON CONFLICT DO NOTHING`. Quando há conflito, uma segunda consulta
é deliberada: sob `READ COMMITTED`, ela recebe um novo snapshot e enxerga a
transação concorrente que acabou de confirmar a mesma chave.

Migrações usam uma tabela de versões e `pg_advisory_xact_lock`, evitando que
duas réplicas tentem aplicar a mesma mudança simultaneamente.

## Consequências

A política é consistente entre réplicas porque a unicidade está no banco, e não
em memória. O cliente precisa preservar a chave entre retries e não pode
reutilizá-la para outro conteúdo.

Ainda não existe entrega externa. A linha com status `pending` será consumida
pelo worker pool em um incremento posterior.
