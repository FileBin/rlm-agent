# Промпты агентов rlm-agent

- `planner.md` — Планировщик, UID 2000: пишет `plan/` (`todo.md`, `architecture.md`) и `knowledge_base/`; объявляет `CRITICAL_PAUSE` + `plan/user_questions.json`.
- `developer.md` — Разработчик, UID 2001: читает `plan/` как контракт, пишет только `code/`, ведёт `code/.scratchpad.md`.
- `tester.md` — Тестировщик, UID 2002: black-box по `code/`, проверки в `checks` + `nix flake check`, отчёт в `reports/`, код не правит.
- `templates/notes.md` — скретчпад всех ролей: шаг, статус, блокеры, следующий шаг; перезапись целиком, ≤ 30 строк.
- Контракт: план → код → отчёт; QA готовит `reports/checks.nix` + `reports/flake.patch`, применяет Разработчик.
- Тупик: `CRITICAL_PAUSE` + `plan/user_questions.json` (`{"questions":[{"id":1,"text":"..."}]}`), дальше ждёт человек.
- Формат ответа (теги) пока не зафиксирован: статус + изменения файлов + команды.
