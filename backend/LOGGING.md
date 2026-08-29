# Backend logs

The backend writes newline-delimited JSON to standard output. Fly collects this output automatically.

## Read the logs

Run this command from the repository root:

```bash
fly logs --config backend/fly.toml
```

Use `jq` to select one trace or operation:

```bash
fly logs --config backend/fly.toml --json | jq 'select(.trace_id == "TRACE_ID")'
fly logs --config backend/fly.toml --json | jq 'select(.operation_id == "OPERATION_ID")'
```

A `trace_id` connects work to an inbound request. An `operation_id` connects records from one model call, database action, parse, or job.

## Payload records

Large payloads use ordered chunks. This prevents Fly from cutting off one large log line.

Each payload record contains these fields:

- `operation_id`
- `direction`
- `content_type`
- `encoding`
- `chunk_index`
- `chunk_count`, when the count is known
- `payload_bytes`, when the original size is known
- `payload`

Sort the records by `chunk_index` to rebuild a payload. Base64-decode the payload when `encoding` is `base64`.

## Coverage

The backend logs these operation boundaries:

- inbound HTTP requests and responses
- outbound HTTP requests and responses
- PostgreSQL queries, batches, connections, and pool actions
- LLM requests, responses, usage, cost, duration, and errors
- embedding inputs, vectors, cache results, retries, and provider results
- memory indexing, search, briefing, mirroring, and re-embedding
- source parsing, AST results, graph windows, and graph publication
- SAST commands, standard output, standard error, and findings
- pipeline stage inputs, outputs, transitions, and failures
- worker start, stop, cycle, and failure records

The logger removes usable credentials from HTTP headers, query parameters, and provider-key request bodies. Database arguments remain complete, including credential-bearing values, as requested. The logger does not remove source code, prompts, diffs, model output, or vectors.

## pgContext and pgGraph

The logs show extension detection and actual runtime selection.

For memory operations, look for these messages:

- `memory vector column type probed`
- `memory search vector engine selected`
- `memory index vector engine selected`

For code-graph operations, look for these messages:

- `pgGraph availability probed`
- `pgGraph projection rebuild started`
- `pgGraph projection rebuild completed`
- `blast radius traversal started`
- `blast radius completed`
- `pgGraph traversal failed; retrying with recursive CTE`

Example pgContext selection:

```json
{"level":"INFO","msg":"memory search vector engine selected","installation_id":42,"engine":"pgcontext","strategy":"exact","vector_query":true,"embedding_space":"v1:..."}
```

Example pgGraph selection and use:

```json
{"level":"INFO","msg":"pgGraph availability probed","pggraph_available":true,"selected_engine":"pggraph","duration_ms":3}
{"level":"INFO","msg":"blast radius traversal started","engine":"pggraph","repo_id":91,"installation_id":42,"seed_file_count":3,"max_depth":2}
{"level":"INFO","msg":"blast radius completed","engine":"pggraph","repo_id":91,"installation_id":42,"seed_file_count":3,"result_count":27,"max_depth":2,"duration_ms":11}
```

If pgGraph is not available, `selected_engine` and the traversal `engine` show `recursive_cte`.

## Example

These records belong to one request. Large model and vector values continue in later chunks.

```json
{"time":"2026-08-12T04:31:19.084Z","level":"INFO","msg":"LLM completion started","operation_id":"a89db603438478d9","trace_id":"82c9ab8c-e45a-4d0d-97cf-acde5c9200ac","provider":"openrouter","model":"anthropic/claude-sonnet-4.6","stage":"review","message_count":2,"tool_count":1,"max_tokens":8000}
{"time":"2026-08-12T04:31:24.921Z","level":"INFO","msg":"LLM completion response","operation_id":"a89db603438478d9","direction":"response","content_type":"application/json","encoding":"utf-8","chunk_index":1,"chunk_count":1,"payload_bytes":241,"payload":"{\"Content\":\"[{...}]\",\"TokensUsed\":{\"PromptTokens\":9132,\"CompletionTokens\":684},\"Cost\":0.0321}"}
{"time":"2026-08-12T04:31:25.114Z","level":"INFO","msg":"embedding operation completed","operation_id":"0f19fdb8c84f89a2","provider":"embeddings:api.voyageai.com","model":"voyage-4","input_count":12,"vector_count":12,"cache_hits":3,"cache_misses":9,"duration_ms":317}
{"time":"2026-08-12T04:31:25.115Z","level":"INFO","msg":"embedding response vectors","operation_id":"0f19fdb8c84f89a2","direction":"response","content_type":"application/json","encoding":"utf-8","chunk_index":1,"chunk_count":8,"payload_bytes":47831,"payload":"{\"model\":\"voyage-4\",\"vectors\":[[0.01827,-0.04311,0.00692,...]]}"}
{"time":"2026-08-12T04:31:25.402Z","level":"DEBUG","msg":"postgres operation","trace_id":"82c9ab8c-e45a-4d0d-97cf-acde5c9200ac","pgx_level":"info","operation":"Query","details":{"sql":"UPDATE reviews SET status=$2 WHERE id=$1","args":["319a...","completed"],"commandTag":"UPDATE 1"}}
{"time":"2026-08-12T04:31:26.731Z","level":"INFO","msg":"source graph parse completed","operation_id":"55112dbeea86f407","file":"internal/auth/handler.go","language":"go","parser":"go_ast","symbol_count":14,"edge_count":37,"duration_ms":2}
```
