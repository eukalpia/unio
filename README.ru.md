# Unio

**S3-совместимое объектное хранилище в одном бинарнике на Go. Быстрое на мелких файлах и листингах, бережное к данным.**

> **Статус: 0.1, ранняя версия.** С 25 ноября 2026 года Unio — единственное
> хранилище учебной платформы университета: около 580 тысяч объектов (видео,
> документы, фото), перенесённых с MinIO через `unio-mirror`. Формат на диске
> ещё может измениться до 1.0. Держите резервные копии.

## Сравнение с MinIO

Одна машина, одни диски, один клиент, запуски друг за другом
(4 vCPU, один SSD, 1 узел × 4 диска, настройки по умолчанию у обоих):

| Нагрузка | MinIO | Unio | |
|---|---:|---:|---:|
| PUT 4 КиБ, 16 клиентов | 801 оп/с | 8 512 оп/с | **×10,6** |
| GET 4 КиБ, 16 клиентов | 6 511 оп/с | 15 962 оп/с | **×2,5** |
| Смешанная GET и PUT, 4 КиБ | 1 711 оп/с | 11 493 оп/с | **×6,7** |
| LIST p99, 20 000 ключей | 859 мс | 145 мс | **×5,9** |
| PUT 1 МиБ | 226 МиБ/с | 371 МиБ/с | **×1,6** |
| GET 1 МиБ | 2 403 МиБ/с | 3 678 МиБ/с | **×1,5** |
| GET 64 МиБ | 5 466 МиБ/с | 6 609 МиБ/с | **×1,2** |
| PUT 64 МиБ | 644 МиБ/с | 649 МиБ/с | наравне (упор в диск) |

Окружение, скрипт, сырые результаты и ограничения замера —
в [bench/results/2026-11-26](bench/results/2026-11-26/README.md).

## Быстрый старт

```sh
docker run --rm -u "$(id -u)" -v "$PWD:/w" ghcr.io/eukalpia/unio:0.1 init --output /w/unio.env
docker run -d --name unio -p 9000:9000 --env-file unio.env \
  -e UNIO_ENDPOINTS='https://localhost:9100/data{1...4}' \
  -e UNIO_NODE_ID=localhost -e UNIO_REQUIRE_TLS=false \
  -v unio1:/data1 -v unio2:/data2 -v unio3:/data3 -v unio4:/data4 \
  ghcr.io/eukalpia/unio:0.1
```

Ключ и секрет для S3-клиента — `UNIO_ROOT_USER` и `UNIO_ROOT_PASSWORD` из
`unio.env`. Переход с MinIO — [docs/migrate-from-minio.md](docs/migrate-from-minio.md).

Подробнее, на английском: [README.md](README.md),
[архитектура](docs/design/architecture.md), [roadmap](ROADMAP.md),
[как контрибьютить](CONTRIBUTING.md).

Unio — независимый проект, не связан с MinIO, Inc. и Amazon Web Services.
Лицензия Apache 2.0.
