# Cloud Storage

Облачное хранилище файлов поверх S3-совместимого объектного хранилища.

| Компонент            | Технология                    | Адрес                   |
|----------------------|-------------------------------|-------------------------|
| Frontend             | JS (vanilla) + nginx          | http://localhost:8080   |
| Backend (REST API)   | Go                            | через nginx: `/api/*`   |
| Объектное хранилище  | VersityGW (S3 API поверх POSIX) | http://localhost:7070 |
| Веб-интерфейс хранилища | VersityGW WebUI (вход по `S3_ACCESS_KEY` / `S3_SECRET_KEY`) | http://localhost:7071 |
| БД                   | MongoDB                       | только внутри сети      |
| Документация API     | Swagger UI (OpenAPI 3)        | http://localhost:8080/swagger/ |
| Метрики              | Prometheus (+ statsd-exporter для VersityGW) | http://localhost:9090 |
| Дашборды и оповещения | Grafana Alerting             | http://localhost:3000 (admin/admin) |

## Запуск

```bash
cp .env.example .env   # поменяйте секреты
docker compose up -d --build
```

Откройте http://localhost:8080 и зарегистрируйтесь.

## Структура проекта

```
cmd/server/              точка входа backend (main.go)
internal/
  app/api/               HTTP-хендлеры, роутинг, middleware
  auth/                  JWT и хеширование паролей
  config/                конфигурация из переменных окружения
  metrics/               метрики Prometheus
  repository/mongo/      MongoDB: пользователи, файлы, оповещения
  storage/s3/            клиент S3 (VersityGW)
api/                     спецификация OpenAPI (встраивается в бинарник)
web/                     frontend: static/ + nginx.conf + Dockerfile
deployments/Dockerfile   образ backend
configs/                 Prometheus и provisioning Grafana (дашборд, алерты)
docker-compose.yml       запуск всего стека
```

Локальный запуск backend без Docker: `go run ./cmd/server` (нужны MongoDB и S3, см. переменные
в `internal/config/config.go`).

## Возможности

- регистрация и вход (bcrypt + JWT);
- загрузка файлов с прогрессом и drag&drop; файлы стримятся в S3 (multipart upload), не буферизуются целиком в памяти;
- квота на пользователя (`USER_QUOTA_BYTES`) и лимит размера файла (`MAX_UPLOAD_BYTES`);
- скачивание, просмотр в браузере (изображения/видео/аудио/текст), переименование, удаление, поиск;
- публичные ссылки (`/api/public/{token}`) с возможностью закрыть доступ;
- вкладка «Оповещения» с алертами из Grafana.

## Хранение данных

- Метаданные файлов, пользователи и история оповещений хранятся в MongoDB (коллекции `users`, `files`, `alerts`).
- Содержимое файлов лежит в бакете `S3_BUCKET` в VersityGW, ключ — `users/<userID>/<fileID>`.

## Мониторинг и оповещения

Backend отдаёт метрики на `/metrics` (HTTP-запросы и задержки, трафик хранилища, операции S3,
доступность MongoDB/S3, пользователи/файлы/занятое место, события аутентификации).
VersityGW шлёт свои метрики по StatsD, `statsd-exporter` переводит их в формат Prometheus.

Grafana автоматически получает источник данных, дашборд «Cloud Storage — обзор» и правила алертов
(`configs/grafana/provisioning/alerting/rules.yml`):

| Алерт                  | Условие                                          |
|------------------------|--------------------------------------------------|
| BackendDown            | Prometheus не может собрать метрики backend      |
| ObjectStorageDown      | VersityGW недоступен                             |
| MongoDBDown            | MongoDB недоступна                               |
| HighErrorRate          | > 5% ответов 5xx за 5 минут                      |
| HighApiLatency         | p95 > 1 c                                        |
| StorageUsageHigh       | квоты заполнены > 80%                            |
| StorageOperationErrors | > 5 ошибок S3 за 5 минут                         |
| LoginBruteforce        | > 20 неудачных входов за 5 минут                 |

Контактная точка — webhook в backend (`POST /api/alerts/webhook`, Bearer `ALERT_WEBHOOK_TOKEN`).
Backend сохраняет оповещения в MongoDB и показывает их во вкладке «Оповещения».
Чтобы получать оповещения ещё и в Telegram/почту/Slack, добавьте receiver в
`configs/grafana/provisioning/alerting/contact-points.yml`.

Проверить вручную: `docker compose stop versitygw` — примерно через минуту появится алерт
`ObjectStorageDown`; `docker compose start versitygw` — он перейдёт в статус «Решено».

## API

| Метод  | Путь                         | Описание                          |
|--------|------------------------------|-----------------------------------|
| POST   | `/api/auth/register`         | `{email, password}` → `{token}`   |
| POST   | `/api/auth/login`            | `{email, password}` → `{token}`   |
| GET    | `/api/me`                    | профиль и использование квоты     |
| GET    | `/api/files?q=`              | список файлов / поиск             |
| POST   | `/api/files`                 | загрузка (multipart, поле `file`) |
| GET    | `/api/files/{id}/download`   | скачивание (`?inline=1` — просмотр) |
| PATCH  | `/api/files/{id}`            | переименование `{name}`           |
| DELETE | `/api/files/{id}`            | удаление                          |
| POST/DELETE | `/api/files/{id}/share` | открыть/закрыть публичную ссылку  |
| GET    | `/api/public/{token}`        | скачивание по публичной ссылке    |
| GET    | `/api/alerts`                | последние оповещения              |

Защищённые методы принимают `Authorization: Bearer <token>`.

Полное описание — в Swagger UI: http://localhost:8080/swagger/ (спецификация:
`api/openapi.yaml`, отдаётся по `/api/openapi.yaml`). Чтобы вызывать методы прямо
из Swagger, выполните `login`, скопируйте `token` и вставьте его в **Authorize → bearerAuth**.
Тест `go test ./internal/app/api/` проверяет, что спецификация совпадает с маршрутами backend.

## Веб-интерфейс VersityGW

http://localhost:7071 — встроенный интерфейс шлюза для просмотра бакетов и объектов.
Объекты там называются `users/<userID>/<fileID>`: настоящие имена файлов хранятся в MongoDB.

## Синхронизация с хранилищем

Данные о файлах (имя, владелец, размер, публичная ссылка) хранятся в MongoDB, содержимое — в VersityGW.
Если объект удалён в обход приложения (через веб-интерфейс VersityGW или S3 API), приложение узнаёт
об этом так:

- **события VersityGW** — шлюз шлёт уведомления (`--event-webhook-url`) на внутренний эндпоинт
  backend `POST /internal/s3-events?token=<S3_EVENT_TOKEN>`. На `s3:ObjectRemoved:*` backend удаляет
  запись о файле и возвращает место в квоту владельца. Эндпоинт не проксируется nginx и доступен
  только внутри Docker-сети;
- **при скачивании** — если событие было пропущено (например, backend не работал) и объекта уже нет,
  запись удаляется, а клиент получает 404 «file no longer exists in storage».

Ограничения:

- VersityGW при старте отправляет тестовое событие и не запускается, пока backend недоступен, поэтому
  он стартует после backend (backend создаёт бакет в фоне, когда шлюз поднимется);
- события, отправленные, пока backend не работал, теряются — такие расхождения обнаруживаются при скачивании;
- файлы, загруженные через веб-интерфейс VersityGW, в приложении не появятся: у них нет имени и владельца.

Метрики: `cloud_storage_events_total{event}`, `cloud_storage_sync_removed_files_total{source="event|download"}`
(панель «Синхронизация с хранилищем» на дашборде).

## docker-compose.override.yml

Задаёт подсеть Docker-сети вручную (`100.100.0.0/24`). Нужен, если VPN занимает все приватные
диапазоны и `docker compose up` падает с `all predefined address pools have been fully subnetted`.
Если у вас такой проблемы нет — файл можно удалить.
