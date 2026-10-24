# Субагенты rlm-agent

Все субагенты — Hermes `delegate_task` на модели **`deepseek-flash`** (дешевле родительской `deepseek-v4-pro`).
Родитель делает минимум: планирование, точное ТЗ, ревью и коммиты.

## Правила

- Модель субагентов: `deepseek-flash` (pinned через `delegation.model` в `~/.hermes/config.yaml`).
- Субагент **не коммитит**: возвращает файлы + команду проверки. Родитель проверяет (`go test` / `nix eval`) и коммитит одним мелким коммитом.
- Откат = удалить последний коммит: `git reset --hard HEAD~1`.
- Один субагент = одна изолированная единица работы (несколько файлов по одной теме).
- **`nn.home` под нагрузкой**: субагенты НЕ шлют реальные запросы к vLLM; тесты — только на `httptest`-фейке.

## Состав и порядок

| # | Субагент | Задача | Артефакты | Проверка | Статус |
|---|---|---|---|---|---|
| 1 | `go-core` | Стейт-машина, `user_questions.json`, бюджет ретраев | `internal/state`, `internal/questions`, `internal/budget` | `go test ./...` | ✅ |
| 2 | `go-exec` | Runner (`nixos-container`+`runuser`) + канальный логгер | `internal/exec`, `internal/log` | `go test ./...` | ✅ |
| 3 | `container` | `flake.nix` + `containers.<name>`: 3 UID, POSIX, `privateNetwork` | `flake.nix`, `nix/containers/*.nix` | `nix eval .#…` | ✅ |
| 4 | `prompts` | Системные промпты Planner/Dev/QA + шаблон заметок | `prompts/*.md` | ревью | ✅ |
| 5 | `llm-client` | OpenAI-совместимый клиент: vLLM + DeepSeek, structured output | `internal/llm/*` | `go test ./...` (httptest) | ⬜ |
| 6 | `orchestrator` | Оркестрационный цикл (mock-LLMClient) | `internal/orchestrator/*`, `cmd/` | `go test ./...` | ⬜ |
| 7 | `memory` | Перенос паттернов в knowledge_base + агрегация ошибок | `internal/memory/*` | `go test` | ⬜ |
| 8 | `tests` | Синтетика, стресс зацикливания, боевая `mkWineApp` | `tests/*` | `nix flake check` (нужен деплой + инференс) | ⬜ |

## Порядок запуска

`llm-client` → `orchestrator` → `memory` → `tests`.

`go-core`/`go-exec`/`container`/`prompts` готовы. `llm-client` и `orchestrator` проверяются локально (httptest + mock) без нагрузки на `nn.home`. `tests` требует деплоя контейнера (root) и свободного `nn.home`.
