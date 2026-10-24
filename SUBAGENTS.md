# Субагенты rlm-agent

Все субагенты — Hermes `delegate_task` на модели **`deepseek-flash`** (дешевле родительской `deepseek-v4-pro`).
Родитель делает минимум: планирование, точное ТЗ, ревью и коммиты.

## Правила

- Модель субагентов: `deepseek-flash` (pinned через `delegation.model` в `~/.hermes/config.yaml`).
- Субагент **не коммитит**: возвращает файлы + команду проверки. Родитель проверяет (`go test` / `nix flake check`) и коммитит одним мелким коммитом.
- Откат = удалить последний коммит: `git reset --hard HEAD~1`.
- Один субагент = одна изолированная единица работы (несколько файлов по одной теме).

## Состав и порядок

| # | Субагент | Задача | Артефакты | Проверка |
|---|---|---|---|---|
| 1 | `go-core` | Стейт-машина, `os/exec` в контейнер, `CRITICAL_PAUSE` + `user_questions.json`, бюджет ретраев, логгер | `go.mod`, `internal/`, `cmd/` | `go build ./... && go test ./...` |
| 2 | `container` | `flake.nix` + `containers.<name>`: 3 UID, POSIX, сетевой режим | `flake.nix`, `nix/containers/*.nix` | `nix flake check` (eval) |
| 3 | `gbnf` | GBNF-грамматики + харнесс спайка | `grammars/*.gbnf`, `scripts/gbnf_spike.*` | прогон против llama-server (нужен A100-эндпоинт) |
| 4 | `prompts` | Системные промпты Planner/Dev/QA + шаблоны заметок | `prompts/*.md` | ревью |
| 5 | `memory` | Перенос паттернов в knowledge_base + агрегация ошибок `flake check` | `internal/memory/*` | `go test` |
| 6 | `tests` | Синтетика, стресс зацикливания, боевая `mkWineApp` | `tests/*` | `nix flake check` |

## Порядок запуска

`go-core` → `container` → `gbnf` (после доступа к A100) → `prompts` → `memory` → `tests`.

Причина: `go-core` целиком проверяется локально (`go test`), `container` требует root для деплоя (проверяется eval-ом), `gbnf` заблокирован отсутствием llama-server на этом хосте.
