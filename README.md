# sozvon-konspekt

Small stateless Go service for one Yandex Disk folder.

## What it does

- Polls one folder on Yandex Disk.
- Finds `.m4a` files that do not have a completed `.txt` result.
- Downloads audio, sends prompt plus audio to OpenRouter on the configured Gemini model, and uploads the final text.
- Keeps running on transient network errors.

## Config

Copy [.env.example](.env.example) to `.env` for local dev. The binary prefers
a bare `.env` if present; otherwise it falls back to the alphabetically-first
`.env.<id>` file in the working directory (e.g. `.env.95-182-83-180` for a
specific server), skipping `.env.example`. Naming a server's file `.env.<id>`
instead of plain `.env` is optional but makes it obvious at a glance which
environment it belongs to when several deploys share the same server. If
neither exists, the process refuses to start.

Copy [example.transcription.prompt.txt](example.transcription.prompt.txt) to `transcription.prompt.txt`.

Change these values:

- `YADISK_CLIENT_ID`
- `YADISK_CLIENT_SECRET`
- `YADISK_FOLDER`
- `OPENROUTER_API_KEY`

Optional OpenRouter values:

- `OPENROUTER_MODEL`, default `google/gemini-2.5-pro`
- `TRANSCRIPTION_PROMPT_FILE`, default `transcription.prompt.txt`

Keep this value as is:

- `YADISK_REDIRECT_URI=https://oauth.yandex.ru/verification_code`

Optional values:

- `YADISK_REFRESH_TOKEN`
- `POLL_INTERVAL` in seconds, default `60`
- `HTTP_TIMEOUT` in seconds, default `120`
- `LOG_LEVEL`: `debug`, `info`, `warn`, `error`

The prompt text is stored outside `.env` in `transcription.prompt.txt`.
Commit the template file [example.transcription.prompt.txt](example.transcription.prompt.txt).

## First start

Only one authorization mode is supported: `https://oauth.yandex.ru/verification_code`.

How it works:

1. Start the service.
2. It prints an authorization URL.
3. Open that URL in the browser and approve access.
4. Yandex shows a verification code.
5. Copy that code, paste it into the terminal, and press Enter.

After the first successful authorization, the service saves `YADISK_REFRESH_TOKEN` to
whichever env file it loaded at startup.
On the next starts it uses that saved refresh token and usually does not ask for browser authorization again.

## Run

```bash
cp .env.example .env
cp example.transcription.prompt.txt transcription.prompt.txt
go run ./cmd/sozvon-konspekt
```

Or build a binary:

```bash
mkdir -p bin
go build -o ./bin/sozvon-konspekt ./cmd/sozvon-konspekt
./bin/sozvon-konspekt
```

## Deploy

For production deploy via CI (push to a `deploy-<id>` branch) and the
one-time server bootstrap, including the Yandex OAuth flow over SSH, see
[DEPLOY.md](DEPLOY.md).

## Logs

`info` logs show:

- poll start and finish
- when folder listing is requested and when it is blocked by OAuth
- folder scan summary
- OAuth bootstrap and token refresh steps
- per-file processing stages
- retry events
