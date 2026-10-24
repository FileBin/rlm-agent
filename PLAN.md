# Автономная многоагентная ИИ-фабрика на NixOS

> Прежнее название «RLM-контейнер» опущено: «рекурсивной языковой модели» как понятия нет — это оркестратор агентов.

Оркестратор на Go управляет тремя LLM-агентами (Планировщик, Разработчик, Тестировщик), которые исполняют код в изолированном `nixos-container` и валидируют результат через `nix flake check`. Инференс — vLLM (OpenAI-совместимый API) на `http://nn.home/v1`, fallback — DeepSeek API.

## Ключевые решения

| Слой | Решение |
|---|---|
| Инференс | vLLM на `http://nn.home/v1` (`qwen3.8-flash-next`), fallback DeepSeek API; stateless-вызовы |
| Структурированный вывод | vLLM — JSON-schema (guided decoding); DeepSeek — только JSON-mode, схема в промпте |
| Оркестратор | Go: goroutines + `os/exec` |
| Песочница | `nixos-containers` (systemd-nspawn); `/nix/store` хоста проброшен read-only |
| Роли | planner (2000), developer (2001), tester (2002) — изоляция через POSIX DAC |
| Валидация | `nix flake check` (только для Nix-задач) |
| Вмешательство | `CRITICAL_PAUSE` + `user_questions.json` + human-in-the-loop |
| Память | файловая база `/workspace/knowledge_base/` |
| Защита от зацикливания | убывающий бюджет ретраев |

## Компоненты

- **Планировщик** (LLM) — декомпозиция задачи в `/workspace/plan/` (`todo.md`, `architecture.md`), ведение knowledge_base, объявление паузы при невыполнимости или нехватке данных.
- **Разработчик** (LLM) — пишет код в `/workspace/code/`, сверяясь с `plan/` как контрактом.
- **Тестировщик** (LLM) — black-box аудит; результаты для Nix-задач проверяются `nix flake check`.
- **Оркестратор** (Go) — стейт-машина `Request → Planning → Dev/QA Loop → Review → Success/Pause`, запуск команд в контейнере от нужного UID (`nixos-container run … runuser -u <role>`), лимит циклов, логирование через `chan`.
- **LLM-клиент** (Go) — OpenAI-совместимый `chat/completions`, два бэкенда: vLLM и DeepSeek, подключаемый `response_format`.

Роли изолированы на уровне промптов (отдельные контексты) и файлов (каждый пишет только в свою папку). Тестировщик при этом *читает* код разработчика — иначе нечего тестировать.

## Известные ограничения (не перепродавать)

- **Не детерминировано**: сэмплинг LLM стохастичен; `flake check` — проверка, а не гарантия корректности.
- **Structured output неодинаков**: vLLM даёт жёсткую JSON-schema (guided decoding), DeepSeek — только `response_format: {"type":"json_object"}` без схемы (схему держит промпт). Клиент это абстрагирует, но строгость разная.
- **`nn.home` под нагрузкой**: реальные запросы — только по готовности; тесты клиента на `httptest`-фейке.
- **Сеть контейнера**: `privateNetwork = true` — отдельный netns *без* интернета (без NAT). Конфликтует с fetch в `flake check` и `winetricks`. Решение: pre-fetch в host-store или выборочный NAT.
- **Запись в store**: `/nix/store` шарится read-only; для сборки в `flake check` нужен проброс unix-сокета nix-демона хоста или тёплый store.
- **POSIX DAC ≠ «физическая» изоляция**: root в контейнере читает всё. Это защита от случайной перезаписи, не MAC.

## Roadmap

1. ✅ **Песочница** — `containers.<name>`: 3 UID, POSIX, `privateNetwork`.
2. ✅ **Промпты** — системные промпты трёх агентов + шаблон заметок.
3. 🔄 **LLM-клиент** — OpenAI-совместимый клиент (vLLM + DeepSeek), structured output.
4. 🔄 **Оркестратор (Go)** — стейт-машина ✅, runner ✅, логгер ✅; осталось: оркестрационный цикл + memory.
5. ⬜ **Память** — перенос успешных паттернов в knowledge_base, агрегация ошибок `flake check`.
6. ⬜ **Тесты** — базовая задача → тест на зацикливание → боевая `mkWineApp` (нужен деплой + свободный `nn.home`).

**Дальше**: LLM-клиент → оркестрационный цикл (mock) → memory → e2e.
