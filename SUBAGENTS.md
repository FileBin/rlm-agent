# Субагенты rlm-agent

Все субагенты — Hermes `delegate_task` на модели **`deepseek-flash`** (дешевле родительской `deepseek-v4-pro`).
Родитель делает минимум: планирование, точное ТЗ, ревью и коммиты.

## Правила

- Модель субагентов: `deepseek-flash` (pinned через `delegation.model` в `~/.hermes/config.yaml`).
- Субагент **не коммитит**: возвращает файлы + команду проверки. Родитель проверяет (`go test` / `nix eval`) и коммитит одним мелким коммитом.
- Откат = удалить последний коммит: `git reset --hard HEAD~1`.
- **`nn.home` под нагрузкой**: субагенты НЕ шлют реальные запросы к vLLM; тесты — только на `httptest`-фейке.

## Готово

| Пакет | Что делает | Проверка |
|---|---|---|
| `internal/state` | конечный автомат задачи (Request→Planning→DevQA→Review→Success/Pause) | `go test` |
| `internal/budget` | убывающий бюджет ретраев | `go test` |
| `internal/questions` | `user_questions.json` read/write | `go test` |
| `internal/exec` | runner: `nixos-container run … runuser -u role` | `go test` |
| `internal/log` | канальный логгер (одна горутина-писатель) | `go test` |
| `internal/llm` | OpenAI-совместимый клиент (vLLM + DeepSeek) | `go test` (httptest) |
| `internal/response` | парсер структурированного ответа + JSON-schema | `go test` |
| `internal/orchestrator` | жизненный цикл: пауза, бюджет, обзор | `go test` (фейки) |
| `internal/agent` | адаптер LLM-клиент → агент (промпт + `response_format`) | `go test` (httptest) |
| `nix/containers/factory.nix` | nixos-container: 3 UID, POSIX, `privateNetwork` | `nix eval` |
| `prompts/*.md` | системные промпты planner/developer/tester + шаблон | ревью |

## Осталось

| Субагент | Задача | Блокер |
|---|---|---|
| `executor` | конкретный Executor (ApplyFiles/RunCommands в контейнер) | нет (тест на построение argv) |
| `cli` | `cmd/orchestrator/main.go`: конфиг (бэкенд/model/key), чтение промптов, пауза-цикл | нет |
| `memory` | перенос паттернов в knowledge_base + агрегация ошибок `flake check` | нет |
| `tests` | синтетика, стресс зацикливания, боевая `mkWineApp` | деплой контейнера (root) + свободный `nn.home` + DeepSeek API key |
