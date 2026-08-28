# ADR 0003: Claim concorrente com lease e fencing token

- Status: aceito
- Data: 2026-08-28

## Contexto

Mais de um worker precisa consumir submissões pendentes sem executar o mesmo
webhook em paralelo. Um simples campo `processing` não é suficiente: se o
processo cair depois do claim, a linha ficaria presa indefinidamente. Também é
possível que um worker lento tente concluir uma tarefa já retomada por outro.

## Decisão

O repositório seleciona uma linha elegível com `FOR UPDATE SKIP LOCKED` e a
atualiza atomicamente para `processing`. Cada claim registra:

- identificador do worker;
- horário do lock;
- contador de tentativas;
- novo `lease_token` aleatório.

Linhas com lease expirado voltam a ser elegíveis. A expiração usa `NOW()` do
próprio PostgreSQL para não depender de relógios sincronizados entre réplicas.
As operações de conclusão e falha exigem o token atual; um worker com token
antigo recebe `ErrLeaseLost` e não consegue sobrescrever o resultado do novo
proprietário.

O pacote `internal/worker` executa uma quantidade fixa de loops concorrentes.
Fila e processador são interfaces, mantendo a política de concorrência testável
sem PostgreSQL ou rede.

## Consequências

Claims concorrentes não se bloqueiam e tarefas abandonadas podem ser retomadas.
O lease pressupõe que o tempo máximo de processamento seja menor que sua
duração; renovação de lease poderá ser adicionada quando houver entregas longas.

Este incremento não inicia o pool na aplicação nem realiza HTTP externo. O
cliente de entrega, controles contra SSRF e política de retries permanecem em
etapas separadas.
