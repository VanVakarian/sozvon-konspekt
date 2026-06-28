# sozvon-konspekt — Deploy

## Как это работает

Деплой = пуш в ветку `deploy-<id>`. CI (`.github/workflows/deploy.yml`) сам
собирает бинарь, заливает на сервер, перезапускает `systemctl restart
sozvon-konspekt`. Ручных шагов в обычном цикле обновления нет.

- `deploy-95` → French (`95.182.83.180`) — тот же сервер, на котором уже
  крутится Flatline в Secondary-режиме (см. `/Users/user/code/flatline`).
- Новый сервер `N` → ветка `deploy-<id>` → GitHub Environment `VPS_<id>`,
  через разовую настройку (раздел ниже).

`release` не трогаем напрямую под деплой — `deploy-<id>` это просто ветка,
синхронизируемая мерджем из `release`. Когда нужно задеплоить очередной
коммит — мерджим/фастфорвардим `release` в `deploy-95`, CI подхватывает пуш.

## Bootstrap нового сервера (один раз, до первого пуша в CI)

CI разворачивает только бинарь — рабочую директорию, env-файл,
`transcription.prompt.txt`, systemd-юнит и зависимости (ffmpeg) готовим
вручную один раз.

```bash
ssh root@<server_ip> install -d -m 755 /root/sozvon-konspekt
```

Если `CONVERT_AUDIO=true` (сжатие звука перед отправкой в модель) — на
сервере должен быть `ffmpeg`:

```bash
ssh root@<server_ip> 'apt-get update && apt-get install -y ffmpeg'
```

Закинуть на сервер (на основе `example.env` / `example.transcription.prompt.txt`,
оба гитигнорены, как и на любом локальном инстансе):

```bash
scp -p .env root@<server_ip>:/root/sozvon-konspekt/.env
scp -p transcription.prompt.txt root@<server_ip>:/root/sozvon-konspekt/transcription.prompt.txt
ssh root@<server_ip> chmod 600 /root/sozvon-konspekt/.env
```

`/etc/systemd/system/sozvon-konspekt.service`:

```ini
[Unit]
Description=Sozvon Konspekt
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/sozvon-konspekt
ExecStart=/root/sozvon-konspekt/sozvon-konspekt
Restart=always
RestartSec=3
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
```

```bash
ssh root@<server_ip> 'systemctl daemon-reload && systemctl enable sozvon-konspekt'
```
(без `--now` — бинаря пока нет, первый раз привезёт CI.)

## OAuth-бутстрап Яндекс.Диска (важно, делать до или сразу после первого деплоя)

Приложению для работы нужен `YADISK_REFRESH_TOKEN`. Если его нет в `.env`
(первый запуск) или Яндекс отозвал токен — приложение не может продолжить
без авторизации в браузере, см. `internal/yandexoauth/manager.go`.

Под systemd у процесса нет интерактивного stdin — `journalctl` это покажет
явно: в логе повторяются строки `authorization required` /
`authorization started` с URL, а следом `stdin closed before confirmation
code was entered` и рестарт юнита по `Restart=always`. Это нормальный сигнал
«жду код», а не сломанный деплой.

Процедура — буквально то же самое, что и при локальном запуске, просто по SSH:

1. `ssh root@<server_ip>`
2. `systemctl stop sozvon-konspekt` (если юнит уже стартовал и зациклился)
3. `cd /root/sozvon-konspekt && ./sozvon-konspekt` — запускаем бинарь в
   форграунде прямо в этой SSH-сессии.
4. В лог печатается `authorization started` с `URL` и `redirect URI`.
   Открываем этот URL в своём браузере, логинимся, подтверждаем доступ.
5. Яндекс показывает код подтверждения на экране — копируем его.
6. Вставляем код в тот же SSH-терминал (где бинарь висит в форграунде) и
   нажимаем Enter — это тот же `bufio.Reader(os.Stdin)`, что и при
   локальном запуске.
7. Лог печатает `authorization completed`, `YADISK_REFRESH_TOKEN`
   автоматически сохраняется в `.env` на сервере.
8. `Ctrl+C`, чтобы остановить форграунд-запуск.
9. `systemctl start sozvon-konspekt` — дальше токен только обновляется
   автоматически, новая авторизация не нужна, пока Яндекс не отозвёт доступ.

## Wiring up CI для нового сервера (один раз)

SSH-ключ привязан к серверу, не к приложению — если на сервере уже крутится
Flatline (или что-то ещё) с ключом `github-actions-vps<id>`, переиспользуем
тот же ключ, просто регистрируем его как секрет в **этом** репозитории
(секреты GitHub Environment не шарятся между репозиториями, даже если ключ
один и тот же):

```bash
# 1. Создать GitHub Environment в этом репо.
gh api --method PUT repos/VanVakarian/sozvon-konspekt/environments/VPS_<id>

# 2. Секреты — SSH_KEY прямо из файла ключа, никогда не копипастить руками.
gh secret set HOST --env VPS_<id> -R VanVakarian/sozvon-konspekt --body "<server_ip>"
gh secret set USER --env VPS_<id> -R VanVakarian/sozvon-konspekt --body "root"
gh secret set SSH_KEY --env VPS_<id> -R VanVakarian/sozvon-konspekt < ~/.ssh/github-actions-vps<id>
```

Если ключа под этот сервер ещё нет вообще — см. генерацию и установку ключа
в `flatline/DEPLOY.md`, раздел "Wiring up CI для нового сервера" (шаги 1-2,
5).

Дальше — создать ветку `deploy-<id>` и запушить. Первый прогон сам зальёт
бинарь и сделает `systemctl restart sozvon-konspekt` (требует, чтобы
unit-файл из bootstrap-раздела уже существовал).

## Верификация

```bash
journalctl -u sozvon-konspekt -f
```

Ожидаем регулярные `service started` → `poll completed` циклы, без повторов
`authorization required` / `poll failed`.

## Ручной деплой без CI (фоллбэк, не основной путь)

```bash
GOOS=linux GOARCH=amd64 go build -trimpath -o bin/sozvon-konspekt ./cmd/sozvon-konspekt
ssh root@<server_ip> systemctl stop sozvon-konspekt
scp -p bin/sozvon-konspekt root@<server_ip>:/root/sozvon-konspekt/sozvon-konspekt
ssh root@<server_ip> systemctl start sozvon-konspekt
```

Env-файл, prompt-файл и systemd-юнит при обычном обновлении бинаря трогать
не нужно — оба уже сделаны в bootstrap-разделе выше.

## Полезные команды

```bash
systemctl status sozvon-konspekt
systemctl restart sozvon-konspekt
journalctl -u sozvon-konspekt -f
```
