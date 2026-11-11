# GitLab CI/CD для учебного проекта

Локально в Docker работают GitLab и GitLab Runner. Runner использует Docker
executor: тесты, сборка и деплой выполняются в отдельных контейнерах.
На VPS приложение работает через Docker Compose под существующим SSH-пользователем.

```text
git push origin v1.0.0
  → GitLab → Runner: go test + lint → docker build backend/frontend
  → release.tgz → SSH → VPS: docker load → compose up --no-build
```

VPS не обращается к локальному GitLab, Container Registry не требуется.
Локальная машина должна быть включена и иметь исходящий SSH-доступ к VPS.
Сам Docker Engine установлен на хостах. Для простоты Runner и build-контейнер
используют Docker socket хоста; такой Runner следует использовать только для
своего доверенного проекта. Источник: [Docker socket binding](https://docs.gitlab.com/ci/docker/using_docker_build/#use-docker-socket-binding).

## 1. Запустить GitLab и Runner локально

Нужны Docker Engine и Compose plugin. Команды выполняются из корня проекта.

```bash
cp deployments/gitlab/.env.example deployments/gitlab/.env
make ci-up
```

В Linux добавь в `/etc/hosts`:

```text
127.0.0.1 gitlab.lan
```

GitLab откроется на <http://gitlab.lan:9080>. Первый запуск может занять несколько
минут. Внутри Docker имя `gitlab.lan` разрешается через общую сеть `cloud-ci` —
это также позволяет job-контейнерам получать репозиторий и артефакты.
Подсеть по умолчанию `100.100.1.0/24` отделена от сети приложения и обходит
VPN-маршруты к приватным диапазонам. При конфликте измени `GITLAB_SUBNET` в `.env`.

Первоначальный пароль пользователя `root`:

```bash
make ci-password
```

После входа поменяй пароль и создай пустой проект `cloud`.
Для доступа с другой машины укажи LAN-адрес вместо `127.0.0.1` в `.env`
(`GITLAB_BIND_ADDRESS`) и в её файле hosts.

GitLab, его данные и регистрация Runner сохраняются в именованных Docker-томах.
`make ci-down` останавливает этот стек без удаления томов.
Состояние контейнеров: `make ci-status`. Без записи в hosts веб-интерфейс также
можно открыть через <http://127.0.0.1:9080>; для Git URL с `gitlab.lan` запись нужна.
Версии GitLab и Runner закреплены в `deployments/gitlab/.env.example`.
Установка: [GitLab в Docker](https://docs.gitlab.com/install/docker/installation/),
[Runner в Docker](https://docs.gitlab.com/runner/install/docker/).

## 2. Зарегистрировать Runner

В проекте GitLab: **Settings → CI/CD → Runners → Create project runner**.

- Тег Runner: `cloud-docker`.
- Отключи **Run untagged jobs**.
- Включи **Protected**.

Скопируй полученный authentication token вида `glrt-…`, затем выполни:

```bash
make ci-register
```

В интерактивной регистрации вставь token, введи произвольное имя Runner,
остальные предложенные значения оставь. Команда уже задаёт Docker executor,
образ, socket и сеть. В GitLab Runner должен появиться online.
Если Runner не перечитал конфигурацию, перезапусти его:

```bash
docker compose --env-file deployments/gitlab/.env \
  -f deployments/gitlab/compose.yml restart runner
```

Источник: [регистрация Runner](https://docs.gitlab.com/runner/register/).

## 3. Подготовить существующего пользователя VPS

В командах ниже **VPS_IP** и **VPS_USER** — заглушки: замени их своим адресом
и именем уже существующего пользователя. Создавать отдельного пользователя не нужно.
На VPS нужны Bash, Docker и Compose **2.24.4 или новее**.
Пользователь должен запускать `docker info` без интерактивного sudo.

```bash
ssh VPS_USER@VPS_IP 'mkdir -p ~/cloud/releases; docker info >/dev/null; docker compose version'
scp .env.example VPS_USER@VPS_IP:cloud/.env
ssh VPS_USER@VPS_IP
nano ~/cloud/.env
chmod 600 ~/cloud/.env
```

Перед первым запуском замени пароли MongoDB, S3 и Grafana, `JWT_SECRET`,
`ALERT_WEBHOOK_TOKEN`, `S3_EVENT_TOKEN`. Можно генерировать значения командой
`openssl rand -hex 32`; hex подходит и для пароля MongoDB в URI.
При последующих деплоях этот файл не перезаписывается.
Если приложение уже запущено, сохрани прежние credentials для существующих данных.

Локальная машина/Runner и VPS в примере — **linux/amd64**. Для ARM измени
`BUILD_PLATFORM` в `.gitlab-ci.yml` и используй Runner с поддержкой этой платформы.

## 4. Добавить SSH-ключ и переменные GitLab

Создай локально отдельный ключ без passphrase и добавь публичную часть на VPS:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/cloud-ci -C cloud-ci
ssh-copy-id -i ~/.ssh/cloud-ci.pub VPS_USER@VPS_IP
ssh -i ~/.ssh/cloud-ci VPS_USER@VPS_IP 'docker info >/dev/null'
```

Получать host key нужно на доверенной машине, до запуска CI:

```bash
ssh-keyscan -t ed25519 VPS_IP > /tmp/cloud-vps-known-hosts
ssh-keygen -lf /tmp/cloud-vps-known-hosts
```

Сверь fingerprint с `/etc/ssh/ssh_host_ed25519_key.pub` в консоли VPS.
Не добавляй приватный ключ в Git. В **Settings → CI/CD → Variables** создай:

| Key | Тип | Значение |
|---|---|---|
| `DEPLOY_HOST` | Variable | `VPS_IP` |
| `DEPLOY_USER` | Variable | `VPS_USER` |
| `DEPLOY_PORT` | Variable, необязательно | SSH-порт, по умолчанию `22` |
| `SSH_PRIVATE_KEY` | File | Содержимое `~/.ssh/cloud-ci` |
| `SSH_KNOWN_HOSTS` | File | Содержимое `/tmp/cloud-vps-known-hosts` |

Включи **Protected**. Для ключа используй видимость Visible, если GitLab
отклоняет Masked из-за переносов строк; File-переменная не печатается скриптом.
У содержимого файлов должен быть завершающий перевод строки.
Для нестандартного SSH-порта получай known_hosts через `ssh-keyscan -p PORT …`.

В **Settings → Repository → Protected tags** защити шаблон `*`, разрешив
создание тегов только Maintainer. Pipeline запускается для каждого Git-тега;
защищённые переменные и Runner доступны для protected tags.
В **Settings → CI/CD → General pipelines** включи
**Prevent outdated deployment jobs**. `resource_group: production` уже запрещает
одновременные деплои.

Источник: [SSH в CI/CD](https://docs.gitlab.com/ci/jobs/ssh_keys/).

## 5. Отправить код и первый тег

Добавь SSH-ключ своего GitLab-пользователя в его профиль. Он нужен для Git push
и отличается от ключа деплоя на VPS. Для локального GitLab URL репозитория выглядит так:

```bash
git remote add gitlab ssh://git@gitlab.lan:2222/YOUR_NAMESPACE/cloud.git
```

Сначала закоммить конфигурацию CI/CD и изменения приложения. Сам незакоммиченный
файл `.gitlab-ci.yml` не попадёт в tag pipeline. Затем отправь ветку и тег:

```bash
git push gitlab HEAD:main
git tag -a v1.0.0 -m 'Release v1.0.0'
git push gitlab v1.0.0
```

В **Build → Pipelines** появятся задания `verify → build → deploy`.
`verify` запускает `go test ./...` и `make lint`. `build` собирает два образа с тегом
commit SHA и архивирует их вместе с Compose и конфигурацией мониторинга.
`deploy` отправляет архив по SSH и проверяет запуск, `/readyz` backend и frontend.
Увеличь лимит artifacts в настройках GitLab, если `release.tgz` не помещается.

Секреты, исходники приложения и локальный VPN override в архив не входят.
VPS загружает образы через `docker load`; `--no-build` запрещает сборку.
Сторонние образы MongoDB, S3 и мониторинга при первом запуске скачиваются на VPS.
Container Registry GitLab и доступ VPS в локальную сеть не нужны.

## 6. Проверка и повторный деплой

Приложение: `http://VPS_IP:8080`, Grafana: `http://VPS_IP:3000`.
В учебной конфигурации сохраняются опубликованные порты из `docker-compose.yml`
(включая S3 `7070/7071` и Prometheus `9090`); HTTPS здесь не настраивается.
MongoDB и native gRPC остаются внутри Docker-сети.

Успешный релиз записывается в `~/cloud/current`. Каталоги версий именуются
по номеру pipeline, а проект Compose всегда называется `cloud-storage`, чтобы
сохранялись тома MongoDB, S3 и мониторинга. Если на VPS уже был этот проект с
таким именем, будут использованы его существующие тома.

Для отката к сохранённому релизу выполни на VPS:

```bash
bash ~/cloud/releases/PREVIOUS_PIPELINE_ID/scripts/deploy-release.sh
```

Неудачный деплой завершает job ошибкой и не меняет `current`; запущенные контейнеры
при этом могли уже обновиться, поэтому откат выполняется указанной командой вручную.
Откат не возвращает старое содержимое БД. Обычное обновление может дать короткий
перерыв. Не удаляй используемые каталоги релизов: конфиги мониторинга примонтированы
из них. Не используй `docker compose down -v` для обновления: он удалит данные.
