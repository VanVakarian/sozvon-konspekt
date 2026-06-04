# sozvon-konspekt

Small stateless Go service for one Yandex Disk folder.

## What it does

- Polls one folder on Yandex Disk.
- Finds `.m4a` files that do not have a completed `.txt` result.
- Creates or refreshes an empty `.txt` placeholder.
- Downloads audio, runs the current stub processor, and uploads the final text.
- Keeps running on transient network errors.

The current processor is a stub in [internal/transcriber/transcriber.go](internal/transcriber/transcriber.go).

## Config

Copy [example.env](example.env) to `.env`.

Change these values:

- `YADISK_CLIENT_ID`
- `YADISK_CLIENT_SECRET`
- `YADISK_FOLDER`

Keep this value as is:

- `YADISK_REDIRECT_URI=https://oauth.yandex.ru/verification_code`

Optional values:

- `YADISK_REFRESH_TOKEN`
- `POLL_INTERVAL` in seconds, default `60`
- `PLACEHOLDER_STALE_AFTER` in seconds, default `300`
- `HTTP_TIMEOUT` in seconds, default `120`
- `LOG_LEVEL`: `debug`, `info`, `warn`, `error`

## First start

Only one authorization mode is supported: `https://oauth.yandex.ru/verification_code`.

How it works:

1. Start the service.
2. It prints an authorization URL.
3. Open that URL in the browser and approve access.
4. Yandex shows a verification code.
5. Copy that code, paste it into the terminal, and press Enter.

After the first successful authorization, the service saves `YADISK_REFRESH_TOKEN` to `.env`.
On the next starts it uses that saved refresh token and usually does not ask for browser authorization again.

## Run

```bash
cp example.env .env
go run ./cmd/sozvon-konspekt
```

Or build a binary:

```bash
mkdir -p bin
go build -o ./bin/sozvon-konspekt ./cmd/sozvon-konspekt
./bin/sozvon-konspekt
```

## Logs

`info` logs show:

- poll start and finish
- when folder listing is requested and when it is blocked by OAuth
- folder scan summary
- OAuth bootstrap and token refresh steps
- per-file processing stages
- retry events
