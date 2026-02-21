# IMCS — In-Memory Cache Server

<p align="center">
  <strong>Легковесный Redis-совместимый кеш-сервер на Go</strong><br>
  <em>Нулевые зависимости · RESP протокол · AOF persistence · 1.2M ops/sec</em>
</p>

<p align="center">
  <a href="#быстрый-старт">Быстрый старт</a> ·
  <a href="#команды">Команды</a> ·
  <a href="#архитектура">Архитектура</a> ·
  <a href="#производительность">Производительность</a> ·
  <a href="#docker">Docker</a> ·
  <a href="#сравнение-с-redis">vs Redis</a> ·
  <a href="#лицензия">Лицензия</a>
</p>

---

## Что такое IMCS

IMCS — это высокопроизводительный in-memory кеш-сервер, полностью совместимый с протоколом RESP (Redis Serialization Protocol). Работает с любым Redis-клиентом: `redis-cli`, `go-redis`, `ioredis`, `Jedis`, `redis-py` и другими.

**Основные возможности:**

- 🔌 **RESP протокол** — подключайтесь через любой Redis SDK
- 💾 **AOF persistence** — данные не теряются при перезапуске
- 🔒 **CRC64 checksums** — защита от повреждения журнала
- ♻️ **AOF Rewrite** — автоматическая компактность журнала
- 🧊 **Cold storage** — выгрузка неактивных данных на диск
- ⚡ **64 шарда** — минимальный contention при конкурентном доступе
- 🗑️ **Janitor** — фоновая очистка TTL через min-heap (O(1))
- 🔐 **Аутентификация** — опциональный пароль через `AUTH`
- 🛑 **Graceful shutdown** — корректное завершение по SIGINT/SIGTERM
- 📦 **0 зависимостей** — только стандартная библиотека Go

---

## Быстрый старт

### Установка из исходников

```bash
git clone https://github.com/CaloriaDigital-hub/IMCS.git
cd IMCS
go build -o imcs ./cmd/imcs/
```

### Запуск

```bash
# Стандартный запуск (порт 6380)
./imcs

# С паролем
./imcs -auth mysecretpassword

# Кастомный порт и директория данных
./imcs -port :6379 -dir /var/lib/imcs
```

### Подключение из Go-кода (без сети — главная фишка!)

```go
import "imcs"

func main() {
    // Одна строка — и кеш готов (с AOF persistence)
    db, err := imcs.Open("./data")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Всё как в Redis, но без TCP — прямые вызовы в RAM (~250ns)
    db.Set("user:1", "John", time.Hour)
    db.Set("config", "value", 0)        // вечный ключ

    val, ok := db.Get("user:1")         // "John", true

    db.Incr("counter")                  // 1
    db.IncrBy("counter", 10)            // 11

    db.SetNX("lock", "owner", 10*time.Second) // true (если не было)

    db.MSet("k1", "v1", "k2", "v2")    // массовая запись
    db.Keys("user:*")                   // ["user:1"]
    db.Len()                            // количество ключей

    // Если нужен TCP-сервер — одна строка:
    go db.ListenAndServe(":6380")       // redis-cli подключится
}
```

### Подключение через redis-cli

```bash
redis-cli -p 6380

127.0.0.1:6380> SET hello world
OK
127.0.0.1:6380> GET hello
"world"
127.0.0.1:6380> INCR counter
(integer) 1
127.0.0.1:6380> SET session:abc "userdata" EX 3600
OK
127.0.0.1:6380> TTL session:abc
(integer) 3599
127.0.0.1:6380> KEYS *
1) "hello"
2) "counter"
3) "session:abc"
```

### Подключение из кода

**Go** (`go-redis`):
```go
import "github.com/redis/go-redis/v9"

rdb := redis.NewClient(&redis.Options{
    Addr:     "localhost:6380",
    Password: "mysecret", // если задан -auth
})

rdb.Set(ctx, "key", "value", time.Hour)
val, _ := rdb.Get(ctx, "key").Result()
```

**Python** (`redis-py`):
```python
import redis
r = redis.Redis(host='localhost', port=6380, password='mysecret')
r.set('key', 'value', ex=3600)
print(r.get('key'))
```

**Node.js** (`ioredis`):
```javascript
const Redis = require('ioredis');
const redis = new Redis({ port: 6380, password: 'mysecret' });
await redis.set('key', 'value', 'EX', 3600);
const val = await redis.get('key');
```

---

## Команды

### Строковые операции

| Команда | Синтаксис | Описание |
|---|---|---|
| `SET` | `SET key value [EX sec] [PX ms] [NX\|XX]` | Установить значение |
| `GET` | `GET key` | Получить значение |
| `DEL` | `DEL key [key ...]` | Удалить ключи |
| `SETNX` | `SETNX key value` | Установить, только если не существует |
| `SETEX` | `SETEX key seconds value` | Установить с TTL |
| `MSET` | `MSET key value [key value ...]` | Массовая установка |
| `MGET` | `MGET key [key ...]` | Массовое чтение |
| `INCR` | `INCR key` | Инкремент на 1 |
| `DECR` | `DECR key` | Декремент на 1 |
| `INCRBY` | `INCRBY key delta` | Инкремент на delta |
| `DECRBY` | `DECRBY key delta` | Декремент на delta |
| `APPEND` | `APPEND key value` | Дописать к значению |
| `STRLEN` | `STRLEN key` | Длина строки |

#### Опции SET

```
SET key value [EX seconds] [PX milliseconds] [NX] [XX]
```

- `EX seconds` — установить TTL в секундах
- `PX milliseconds` — установить TTL в миллисекундах
- `NX` — установить только если ключ **не существует**
- `XX` — установить только если ключ **уже существует**

### Управление ключами

| Команда | Синтаксис | Описание |
|---|---|---|
| `EXISTS` | `EXISTS key [key ...]` | Проверить существование (возвращает кол-во) |
| `EXPIRE` | `EXPIRE key seconds` | Установить TTL в секундах |
| `PEXPIRE` | `PEXPIRE key ms` | Установить TTL в миллисекундах |
| `TTL` | `TTL key` | Оставшееся время жизни (секунды) |
| `PTTL` | `PTTL key` | Оставшееся время жизни (миллисекунды) |
| `PERSIST` | `PERSIST key` | Убрать TTL (сделать вечным) |
| `TYPE` | `TYPE key` | Тип значения |
| `RENAME` | `RENAME old new` | Переименовать ключ |
| `KEYS` | `KEYS pattern` | Поиск ключей по glob-паттерну |

#### Коды возврата TTL/PTTL

| Код | Значение |
|---|---|
| `N` (положительное) | Оставшееся время жизни |
| `-1` | Ключ существует, но без TTL |
| `-2` | Ключ не найден |

### Серверные команды

| Команда | Описание |
|---|---|
| `PING [message]` | Проверка соединения |
| `ECHO message` | Эхо |
| `DBSIZE` | Количество ключей |
| `INFO` | Информация о сервере |
| `FLUSHDB` | Очистить все данные |
| `FLUSHALL` | Очистить все данные + cold storage |
| `SELECT db` | Выбор БД (всегда OK) |
| `AUTH password` | Аутентификация |
| `QUIT` | Закрыть соединение |
| `COMMAND` | Информация о командах |
| `CONFIG SET key value` | Установить параметр (заглушка) |
| `CLIENT ...` | Информация о клиенте (заглушка) |

---

## Архитектура

```
┌────────────────────────────────────────────────────────────────────┐
│                          IMCS Server                               │
│                                                                    │
│  ┌──────────────┐    ┌──────────────────────────────────────────┐  │
│  │  TCP Listener │──▶│         RESP Parser (inline + multibulk) │  │
│  │  (port 6380)  │    │         + AUTH check                     │  │
│  └──────────────┘    └────────────────┬─────────────────────────┘  │
│                                       │                            │
│                                       ▼                            │
│  ┌────────────────────────────────────────────────────────────────┐│
│  │                    Command Router                              ││
│  │  SET  GET  DEL  INCR  EXPIRE  TTL  KEYS  MGET  MSET  ...     ││
│  └────────────────────────────┬───────────────────────────────────┘│
│                               │                                    │
│                               ▼                                    │
│  ┌────────────────────────────────────────────────────────────────┐│
│  │              Sharded Cache (64 шарда × RWMutex)               ││
│  │                                                                ││
│  │  ┌────────┐ ┌────────┐ ┌────────┐         ┌────────┐         ││
│  │  │ Shard 0│ │ Shard 1│ │ Shard 2│  . . .  │Shard 63│         ││
│  │  │ map+pq │ │ map+pq │ │ map+pq │         │ map+pq │         ││
│  │  └────────┘ └────────┘ └────────┘         └────────┘         ││
│  └────────────────────────────┬───────────────────────────────────┘│
│                               │                                    │
│              ┌────────────────┴────────────────────┐               │
│              │                                     │               │
│              ▼                                     ▼               │
│  ┌──────────────────────┐            ┌─────────────────────────┐  │
│  │     AOF Persister    │            │     Cold Storage        │  │
│  │  ┌────────────────┐  │            │  (gob files на диске)   │  │
│  │  │ CRC64 + Write  │  │            └─────────────────────────┘  │
│  │  │ Rewrite buffer │  │                       ▲                  │
│  │  │ Fsync 1/sec    │  │                       │                  │
│  │  └────────────────┘  │            ┌─────────────────────────┐  │
│  └──────────────────────┘            │       Janitor           │  │
│                                      │  TTL Expiry (1с, heap)  │  │
│                                      │  Cold Eviction (10с)    │  │
│                                      │  Disk Flush (30с)       │  │
│                                      └─────────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
```

### Ключевые решения

#### Шардирование (64 шарда)

Каждый ключ попадает в один из 64 шардов по хешу FNV-1a. Каждый шард имеет свой `sync.RWMutex`, что обеспечивает минимальный contention при конкурентном доступе тысяч горутин.

#### Min-Heap для TTL

Ключи с TTL хранятся в priority queue (min-heap), отсортированной по `ExpireAt`. При cleanup janitor смотрит только вершину heap — O(1) вместо O(N) полного сканирования.

#### AOF с CRC64

Каждая запись в AOF-журнале содержит контрольную сумму CRC64 (ECMA). При восстановлении проверяется целостность каждой записи. Повреждённые записи отсекаются — файл truncate до последней валидной записи.

#### AOF Rewrite

Пока идёт snapshot → запись нового файла, все новые записи дублируются в `rewriteBuf`. После записи snapshot, буфер дописывается, и файл атомарно заменяется через `os.Rename`. Ни одна запись не теряется.

#### Cold Storage

Данные, не востребованные более 5 минут, автоматически выгружаются на диск (gob). При обращении к ключу — данные поднимаются обратно в RAM. Это позволяет экономить оперативную память.

---

## Производительность

Результаты тестирования на одной машине (столько же горутин = столько же клиентов):

| Тест | Результат |
|---|---|
| **TCP Throughput (RESP)** | **1,205,879 ops/sec** |
| **In-Memory Throughput** | **4,047,963 ops/sec** |
| Avg TCP latency | 829ns/op |
| Avg in-memory latency | 247ns/op |
| INCR throughput | 1,211,479 atomic/sec |
| Pipeline (2000 cmd) | 312,577 ops/sec |
| Big values (GET 1MB) | 917 MB/sec |
| Max connections (tested) | 2,000 simultaneous |
| Mixed chaos (15 commands) | 1,183,341 ops/sec |
| Binary size (stripped) | ~4MB |
| RAM at start | ~5MB |

---

## Benchmarking Environment

Benchmarks were performed on the following hardware and software setup to ensure reproducibility:

## Hardware
*   **CPU:** Intel(R) Core(TM) i9-13900H (14 Cores / 20 Threads)
    *   **Max Turbo Frequency:** 5.40 GHz
    *   **L3 Cache:** 24 MiB
*   **RAM:** 16 GB DDR5 4800 MT/s (2x8GB)
*   **Architecture:** x86_64

## Software

   **OS:** Linux Mint 22.3 (Zena)
   **Kernel:** 6.17.0-14-generic
   **Go Version:** go1.25.6 linux/amd64

## Конфигурация

### Флаги командной строки

| Флаг | По умолчанию | Описание |
|---|---|---|
| `-port` | `:6380` | TCP-адрес и порт для прослушивания |
| `-dir` | `./cache-files` | Директория для AOF-журнала и cold storage |
| `-auth` | `""` | Пароль для команды AUTH (пустой = без аутентификации) |

### Примеры

```bash
# Продакшн: кастомный порт, отдельная директория данных, пароль
./imcs -port :6379 -dir /var/lib/imcs -auth $(openssl rand -hex 16)

# Разработка: стандартные настройки
./imcs

# Тестирование: отдельный порт
./imcs -port :16380 -dir /tmp/imcs-test
```

---

## Docker

### Сборка и запуск

```bash
# Сборка образа
docker build -t imcs .

# Запуск
docker run -d \
  --name imcs \
  -p 6380:6380 \
  -v imcs-data:/data \
  imcs

# С паролем
docker run -d \
  --name imcs \
  -p 6380:6380 \
  -v imcs-data:/data \
  imcs -auth mysecretpassword

# Проверка
redis-cli -p 6380 PING
```

### Docker Compose

```yaml
version: '3.8'
services:
  imcs:
    build: .
    ports:
      - "6380:6380"
    volumes:
      - imcs-data:/data
    command: ["-auth", "${IMCS_PASSWORD:-}"]
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 256M

volumes:
  imcs-data:
```

---

## Тестирование

```bash
# Все тесты
go test ./...

# С выводом
go test -v ./...

# Только server hard тесты
go test -v -run 'TestHard' ./internal/server/

# Только AOF тесты
go test -v ./internal/persistence/AOF/

# С race detector
go test -race ./...
```

### Результаты тестов (17/17 PASS)

| Пакет | Тесты | Результат |
|---|---|---|
| `internal/server` | RESP Protocol (50 cmd), TCP Stress (5M ops), Atomic INCR, Pipeline Burst, TTL Expiry, Big Values (1MB), Max Connections (2000), Mixed Chaos | ✅ 8/8 |
| `internal/persistence/AOF` | CRC64, Corruption Truncate (3 сценария), Rewrite, Crash No Flush, Heavy Load Crash | ✅ 5/5 |
| `internal/storage` | LRU Eviction, LRU Stress, 50K Users Stress, Burst | ✅ 4/4 |

---

## Сравнение с Redis

| Возможность | IMCS | Redis 7.x |
|---|---|---|
| TCP Throughput | **1.2M ops/sec** | ~100-200K ops/sec |
| Binary | ~4MB | ~12MB |
| RAM (старт) | ~5MB | ~10MB |
| Docker image | ~15MB | ~50MB |
| Зависимости | **0** | libc, jemalloc |
| RESP протокол | ✅ | ✅ |
| AOF persistence | ✅ CRC64 | ✅ |
| AOF Rewrite | ✅ | ✅ |
| Cold storage (диск) | ✅ | ❌ |
| Строки | ✅ | ✅ |
| Списки, множества, хеши | ❌ | ✅ |
| Pub/Sub | ❌ | ✅ |
| Lua скрипты | ❌ | ✅ |
| Кластер | ❌ | ✅ |

### Когда использовать IMCS

- **Микросервисы** — встраиваемый кеш без внешних зависимостей
- **Rate limiting** — INCR atomic, 1.2M ops/sec через TCP
- **Сессии** — SET с EX + GET, AOF persistence
- **Счётчики** — INCR/DECRBY, данные не теряются при рестарте
- **Edge / IoT** — 5MB RAM, 4MB binary
- **CI/CD тесты** — мгновенный старт, не нужен Docker Redis

### Когда использовать Redis

- Нужны структуры данных: Sets, Sorted Sets, Hashes, Streams
- Нужен Pub/Sub или Lua скрипты
- Нужен кластер с шардированием по нодам
- Нужно 100K+ одновременных соединений

---

## Участие в разработке

Мы приветствуем вклад в проект! Для участия:

1. Форкните репозиторий
2. Создайте ветку для вашей функции (`git checkout -b feature/my-feature`)
3. Зафиксируйте изменения (`git commit -m 'feat: описание'`)
4. Отправьте ветку (`git push origin feature/my-feature`)
5. Откройте Pull Request

### Рекомендации

- Пишите тесты для новых функций
- Следуйте стилю кода Go (`gofmt`, `go vet`)
- Обновляйте документацию при добавлении команд
- Используйте conventional commits: `feat:`, `fix:`, `docs:`, `test:`

---

## Лицензия

Этот проект распространяется под лицензией **MIT**. Подробности в файле [LICENSE](LICENSE).

---

<p align="center">
  <strong>IMCS</strong> — когда Redis слишком тяжёл, а HashMap недостаточно.<br>
  <em>~4600 строк Go · 0 зависимостей · 17 тестов · 1.2M ops/sec</em>
</p>
