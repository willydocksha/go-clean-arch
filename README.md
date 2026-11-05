# go-clean-arch

Boilerplate REST API Go (1.27) dengan **Clean Architecture**, memakai router [chi](https://github.com/go-chi/chi) (100% kompatibel dengan `net/http`). Sudah termasuk:

- **CRUD contoh** (`products`) sampai ke PostgreSQL: list (filter, search, sort, pagination), detail, create, `PUT` (edit penuh), `PATCH` (edit 1 field), delete
- **Migration** (tanpa tool tambahan, SQL di-embed ke binary) dan **seeder** data contoh
- **Redis**: cache JSON, cache-aside, counter, rate limit, distributed lock, delete by pattern
- **S3** (AWS S3 / MinIO / R2 / dll.): upload, download, delete, presigned URL download & upload
- **CSV & XLSX**: baca dan tulis file, export/import data
- **3 cara mengembalikan file ke frontend**: file langsung (blob), base64 dalam JSON, atau URL dari S3

> Redis dan S3 **opsional**. Cukup punya PostgreSQL, project sudah bisa jalan. Kalau Redis kosong, cache otomatis dimatikan. Kalau S3 kosong, endpoint storage membalas `503`.

---

## Daftar isi

1. [Struktur project](#struktur-project)
2. [Quick start](#quick-start)
3. [Konfigurasi `.env`](#konfigurasi-env)
4. [Migration & seeder](#migration--seeder)
5. [Endpoint](#endpoint)
6. [Mengembalikan file ke frontend](#mengembalikan-file-ke-frontend)
7. [Import & export CSV/XLSX](#import--export-csvxlsx)
8. [S3](#s3)
9. [Redis](#redis)
10. [Menambah modul baru](#menambah-modul-baru)
11. [Testing](#testing)
12. [Troubleshooting](#troubleshooting)

---

## Struktur project

```
cmd/
  api/              → entrypoint HTTP server
  migrate/          → CLI migration (up / down / status)
  seed/             → CLI seeder data contoh
internal/
  domain/           → entity, error, interface (port). tidak import apa pun dari project
  usecase/          → business logic (validasi, cache, export/import)
  repository/
    postgres/       → implementasi repository dengan pgx
    memory/         → implementasi in-memory (dipakai di unit test)
  delivery/http/
    handler/        → HTTP handler (user, product, file, health)
    middleware/     → request id, logger, recoverer, cors
    response/       → format JSON standar + response file/base64
    router.go       → router chi: middleware global, /healthz, /readyz, group /api/v1
  config/           → baca env (otomatis load .env)
  app/              → composition root: menyambungkan semua layer + graceful shutdown
pkg/                → library reusable, bisa di-copy ke project lain
  cache/            → helper redis
  storage/          → client s3
  spreadsheet/      → baca/tulis csv & xlsx
  migrator/         → runner migration sql
  database/         → koneksi pgx pool
  logger/           → slog
migrations/         → file .sql (di-embed ke binary)
```

Arah dependency: `delivery → usecase → domain ← repository`. Usecase hanya tahu **interface** (`domain.Cache`, `domain.FileStorage`, `domain.ProductRepository`), jadi implementasinya bisa diganti tanpa mengubah business logic.

---

## Quick start

### Opsi A: PostgreSQL lokal saja (paling cepat)

Prasyarat: Go 1.27+, PostgreSQL yang sudah jalan.

1. Buat database:

```bash
createdb -U postgres go_clean_arch
```

2. Salin env, lalu sesuaikan `DATABASE_URL` (user/password Postgres kamu):

```bash
cp .env.example .env
```

3. Jalankan migration + seeder sekaligus:

```bash
make setup
```

4. Jalankan API:

```bash
make run
```

5. Coba:

```bash
curl "localhost:8080/api/v1/products?limit=5"
```

### Opsi B: Docker Compose (Postgres + Redis + MinIO + API)

```bash
make up
```

Isi data contoh:

```bash
docker compose run --rm seed
```

- API: http://localhost:8080
- MinIO console: http://localhost:9001 (login `minioadmin` / `minioadmin`)
- Bucket `go-clean-arch` dibuat otomatis saat API start (`S3_CREATE_BUCKET=true`)

### Opsi C: Lokal + Redis/MinIO

Jalankan Redis dan MinIO sendiri (misalnya `brew install redis` atau container), lalu isi bagian Redis/S3 di `.env`:

```dotenv
REDIS_ADDR=localhost:6379

S3_BUCKET=go-clean-arch
S3_REGION=us-east-1
S3_ENDPOINT=http://localhost:9000
S3_ACCESS_KEY_ID=minioadmin
S3_SECRET_ACCESS_KEY=minioadmin
S3_FORCE_PATH_STYLE=true
S3_CREATE_BUCKET=true
```

Cek status dependency:

```bash
curl localhost:8080/readyz
```

```json
{"success":true,"data":{"postgres":"up","redis":"up","s3":"up"}}
```

---

## Konfigurasi `.env`

Semua variabel ada di [`.env.example`](.env.example) lengkap dengan komentar. File `.env` otomatis dibaca oleh `cmd/api`, `cmd/migrate`, dan `cmd/seed`. Environment variable asli selalu menang dari isi `.env`.

| Variabel | Default | Keterangan |
|---|---|---|
| `DATABASE_URL` | – | **wajib**. Connection string Postgres |
| `DB_AUTO_MIGRATE` | `false` | `true` = jalankan migration saat API start |
| `HTTP_PORT` | `8080` | |
| `HTTP_MAX_UPLOAD_MB` | `10` | batas ukuran upload multipart |
| `HTTP_ALLOWED_ORIGINS` | `*` | CORS, pisahkan dengan koma |
| `REDIS_ADDR` | kosong | kosong = Redis mati (tanpa cache) |
| `REDIS_KEY_PREFIX` | `app:` | prefix semua key |
| `S3_BUCKET` | kosong | kosong = S3 mati (endpoint storage → 503) |
| `S3_ENDPOINT` | kosong | kosong untuk AWS; isi untuk MinIO/R2/LocalStack |
| `S3_PRESIGN_ENDPOINT` | kosong | host publik di presigned URL (API di docker, browser di luar) |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | kosong | kosong di AWS = pakai IAM role / `~/.aws` |
| `S3_FORCE_PATH_STYLE` | `false` | `true` untuk MinIO |
| `S3_PRESIGN_EXPIRY` | `15m` | masa berlaku presigned URL |
| `S3_CREATE_BUCKET` | `false` | buat bucket saat start (khusus dev) |

---

## Migration & seeder

File migration ada di `migrations/` dengan format `000001_nama.up.sql` dan `000001_nama.down.sql`. File ini di-embed ke binary, jadi **tidak perlu install golang-migrate**. Versi yang sudah dijalankan dicatat di tabel `schema_migrations`.

| Perintah | Fungsi |
|---|---|
| `make migrate-up` | jalankan semua migration yang belum |
| `make migrate-down` | rollback 1 migration terakhir (`make migrate-down n=2` untuk 2) |
| `make migrate-status` | lihat mana yang sudah/belum |
| `make migrate-new name=create_orders_table` | buat file migration baru (nomor otomatis) |
| `make seed` | isi 5 user + 60 product contoh (aman dijalankan berulang) |
| `make seed-fresh` | kosongkan tabel users & products lalu seed ulang |

Di production cukup jalankan binary `migrate up` sebelum `api`, atau set `DB_AUTO_MIGRATE=true`.

---

## Endpoint

Semua response JSON memakai format yang sama:

```json
{ "success": true, "data": { }, "meta": { "limit": 10, "offset": 0, "total": 60 } }
{ "success": false, "error": "invalid input: name is required; price is required and must be >= 0" }
```

Error domain otomatis jadi status HTTP (route yang tidak ada → `404`, method salah → `405`, juga dalam format JSON): invalid input → `400`, not found → `404`, conflict → `409`, body terlalu besar → `413`, S3 mati → `503`, lainnya → `500` (detail tidak dibocorkan ke client, hanya di-log).

### Health

| Method | Path | Keterangan |
|---|---|---|
| GET | `/healthz` | server hidup |
| GET | `/readyz` | ping Postgres, Redis, S3 (`503` jika ada yang down) |

### Products (contoh CRUD lengkap ke DB)

| Method | Path | Keterangan |
|---|---|---|
| GET | `/api/v1/products` | list. Query: `q`, `category`, `status`, `sort` (`created_at`/`name`/`price`/`stock`), `order` (`asc`/`desc`), `limit`, `offset` |
| GET | `/api/v1/products/{id}` | detail (di-cache Redis 10 menit) |
| POST | `/api/v1/products` | create |
| PUT | `/api/v1/products/{id}` | edit penuh, semua field wajib |
| PATCH | `/api/v1/products/{id}/status` | edit 1 field (`status`) |
| DELETE | `/api/v1/products/{id}` | hapus |
| GET | `/api/v1/products/export` | export CSV/XLSX (filter sama dengan list) |
| GET | `/api/v1/products/import/template` | download template import |
| POST | `/api/v1/products/import` | import CSV/XLSX (upsert berdasarkan `sku`) |

Contoh pemakaian:

```bash
# list: search + filter + sort
curl "localhost:8080/api/v1/products?q=keyboard&category=electronics&status=active&sort=price&order=asc&limit=5"

# detail
curl localhost:8080/api/v1/products/<id>

# create
curl -X POST localhost:8080/api/v1/products -H 'Content-Type: application/json' -d '{
  "sku": "PRD-1001", "name": "Kopi Arabica", "description": "250 gram",
  "category": "food", "price": 85000, "stock": 20, "status": "active"
}'

# put: semua field wajib diisi
curl -X PUT localhost:8080/api/v1/products/<id> -H 'Content-Type: application/json' -d '{
  "sku": "PRD-1001", "name": "Kopi Arabica Gayo", "description": "500 gram",
  "category": "food", "price": 150000, "stock": 10, "status": "active"
}'

# patch: cuma 1 field
curl -X PATCH localhost:8080/api/v1/products/<id>/status -H 'Content-Type: application/json' -d '{"status":"inactive"}'

# delete
curl -X DELETE localhost:8080/api/v1/products/<id>
```

`price` disimpan sebagai bilangan bulat dalam satuan terkecil (rupiah), supaya tidak ada masalah pembulatan float.

### Users (contoh CRUD sederhana)

`GET/POST /api/v1/users` dan `GET/PATCH/DELETE /api/v1/users/{id}`.

### Files (S3 + parser)

| Method | Path | Keterangan |
|---|---|---|
| POST | `/api/v1/files` | upload multipart (`file`, opsional `folder`). Balikan berisi `key` dan presigned URL |
| POST | `/api/v1/files/presign-upload` | minta URL supaya browser upload **langsung** ke S3 |
| GET | `/api/v1/files/url?key=&filename=` | presigned URL download |
| GET | `/api/v1/files/download?key=&delivery=file\|base64` | download lewat API (proxy) |
| DELETE | `/api/v1/files?key=` | hapus object |
| POST | `/api/v1/files/parse?limit=100` | baca CSV/XLSX lalu balikan baris dalam JSON (**tidak butuh S3**) |

---

## Mengembalikan file ke frontend

Endpoint yang menghasilkan file (`/products/export`, `/products/import/template`) menerima `?delivery=`:

| `delivery` | Response | Cocok untuk |
|---|---|---|
| `file` (default) | byte file mentah + `Content-Disposition: attachment` | download langsung |
| `base64` | JSON `{filename, content_type, size, base64}` | FE yang ingin semua response berupa JSON |
| `url` | JSON `{key, url, expires_at, ...}`. File di-upload ke S3, URL berupa presigned | file besar, link bisa dibagikan, tidak membebani API |

```bash
curl -OJ "localhost:8080/api/v1/products/export?format=xlsx"
```

```bash
curl "localhost:8080/api/v1/products/export?format=csv&delivery=base64"
```

```bash
curl "localhost:8080/api/v1/products/export?format=xlsx&delivery=url&status=active"
```

Cara memakainya di frontend (JavaScript):

```js
// 1) delivery=file: response berupa blob
const res = await fetch('/api/v1/products/export?format=xlsx');
const blob = await res.blob();
const name = /filename="?([^"]+)"?/.exec(res.headers.get('Content-Disposition'))?.[1] ?? 'export.xlsx';
const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(blob), download: name });
a.click(); URL.revokeObjectURL(a.href);

// 2) delivery=base64: decode jadi blob
const { data } = await (await fetch('/api/v1/products/export?format=csv&delivery=base64')).json();
const bytes = Uint8Array.from(atob(data.base64), c => c.charCodeAt(0));
const blob2 = new Blob([bytes], { type: data.content_type });
// lalu sama seperti cara 1, pakai data.filename

// 3) delivery=url: buka saja URL-nya
const { data: f } = await (await fetch('/api/v1/products/export?format=xlsx&delivery=url')).json();
window.location.href = f.url; // berlaku sampai f.expires_at
```

Upload langsung dari browser ke S3 tanpa lewat API:

```js
const { data: t } = await (await fetch('/api/v1/files/presign-upload', {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ folder: 'avatars', filename: file.name, content_type: file.type }),
})).json();
await fetch(t.url, { method: t.method, headers: t.headers, body: file });
// simpan t.key di database kamu
```

> Kalau pakai MinIO/S3 dari browser, atur CORS di bucket supaya origin FE diizinkan untuk `PUT`/`GET`.

---

## Import & export CSV/XLSX

- **Export**: `GET /api/v1/products/export?format=csv|xlsx`. File XLSX punya header tebal, baris header di-freeze, autofilter, dan lebar kolom otomatis. File CSV memakai BOM UTF-8 supaya Excel membaca huruf non-ASCII dengan benar. Maksimal 50.000 baris.
- **Template**: `GET /api/v1/products/import/template?format=xlsx`
- **Import**:

```bash
curl -F file=@products.xlsx localhost:8080/api/v1/products/import
```

```json
{"total_rows":3,"created":1,"updated":1,"failed":1,
 "errors":[{"row":4,"sku":"IMP-2","message":"invalid input: name is required"}]}
```

Aturan import:

- Kolom wajib: `sku`, `name`, `category`, `price`, `stock`. Kolom opsional: `description`, `status`.
- Data di-upsert berdasarkan `sku`: SKU yang sudah ada di-update, yang belum ada dibuat baru.
- Baris yang error tidak menghentikan baris lain. Nomor baris di laporan dihitung dari 1, dengan header sebagai baris 1.
- Angka seperti `150.000` atau `150,000` dari Excel tetap terbaca. CSV dengan pemisah `;` (Excel locale Indonesia) terdeteksi otomatis.
- File hasil export bisa langsung di-import lagi.

Untuk dipakai di modul lain, cukup pakai `pkg/spreadsheet`:

```go
// tulis
data, _ := spreadsheet.Encode(spreadsheet.XLSX, spreadsheet.Table{
    Headers: []string{"nama", "nilai"},
    Rows:    [][]string{{"Budi", "90"}},
})

// baca
table, _ := spreadsheet.Read(file, spreadsheet.CSV)
for _, rec := range table.Records() { // []map[string]string
    fmt.Println(rec["nama"])
}
```

---

## S3

`pkg/storage` bisa dipakai di mana saja:

```go
s3c, _ := storage.NewS3(ctx, storage.S3Config{Bucket: "my-bucket", Region: "ap-southeast-1"})

s3c.Upload(ctx, "docs/a.pdf", file, size, "application/pdf")       // upload
data, contentType, _ := s3c.Get(ctx, "docs/a.pdf")                  // baca ke memory
body, info, _ := s3c.Download(ctx, "docs/a.pdf")                    // stream (jangan lupa body.Close())
ok, _ := s3c.Exists(ctx, "docs/a.pdf")
url, _ := s3c.PresignGet(ctx, "docs/a.pdf", 15*time.Minute, "a.pdf") // link download
url, _ = s3c.PresignPut(ctx, "docs/b.pdf", "application/pdf", 15*time.Minute)
items, _ := s3c.List(ctx, "docs/", 100)
s3c.Delete(ctx, "docs/a.pdf")
```

- Key file di-generate server: `<folder>/<yyyy>/<mm>/<dd>/<uuid>.<ext>`. Nama folder divalidasi supaya aman.
- Error "not found" dari S3 dibungkus `fs.ErrNotExist`, jadi cukup dicek dengan `errors.Is(err, fs.ErrNotExist)`.
- Pakai AWS asli: kosongkan `S3_ENDPOINT`, `S3_FORCE_PATH_STYLE=false`, dan isi credential (atau pakai IAM role).

---

## Redis

`pkg/cache` membungkus `go-redis` dengan fungsi yang paling sering dipakai:

```go
rdb, _ := cache.NewRedis(ctx, cache.RedisConfig{Addr: "localhost:6379", KeyPrefix: "myapp:"})

// json get/set
rdb.Set(ctx, "user:1", user, 10*time.Minute)
err := rdb.Get(ctx, "user:1", &user)          // cache.ErrMiss jika tidak ada

// cache-aside: ambil dari cache, kalau tidak ada hitung lalu simpan
stats, err := cache.Remember(ctx, rdb, "stats:today", time.Minute, func(ctx context.Context) (Stats, error) {
    return repo.ComputeStats(ctx)
})

// counter
views, _ := rdb.Incr(ctx, "views:product:1", 1, 24*time.Hour)

// rate limit: 100 request per menit per ip
res, _ := rdb.RateLimit(ctx, "ip:"+ip, 100, time.Minute)
if !res.Allowed { /* 429, coba lagi setelah res.ResetIn */ }

// distributed lock (cron/job supaya tidak jalan dobel)
unlock, err := rdb.Lock(ctx, "job:daily-report", 5*time.Minute)
if errors.Is(err, cache.ErrLockNotAcquired) { return }
defer unlock(ctx)

// idempotency key
first, _ := rdb.SetNX(ctx, "idem:"+requestID, 1, time.Hour)

// hapus banyak key (pakai SCAN, aman untuk production)
rdb.DeleteByPattern(ctx, "product:*")

// fungsi lain: GetString/SetString, Delete, Exists, Expire, TTL, Client() untuk akses go-redis langsung
```

Di modul products, detail product di-cache dengan key `product:{id}`. Cache otomatis dihapus saat PUT, PATCH, DELETE, atau import. Kalau Redis tidak dikonfigurasi, dipakai `cache.Noop{}` sehingga kode usecase tidak perlu `if redis != nil`.

---

## Menambah modul baru

Contoh menambah modul `orders`:

1. `make migrate-new name=create_orders_table`, lalu isi SQL up/down.
2. `internal/domain/order.go`: entity, input, interface `OrderRepository` dan `OrderUsecase`.
3. `internal/repository/postgres/order_repository.go`: query SQL.
4. `internal/usecase/order_usecase.go`: validasi dan business logic. Pakai `domain.Cache` / `domain.FileStorage` kalau perlu.
5. `internal/delivery/http/handler/order_handler.go`: handler + method `Register(r chi.Router)`. Path di sini relatif terhadap `/api/v1`.
6. Sambungkan di `internal/app/app.go` (repo → usecase → handler), lalu masukkan handler ke `NewRouter`.

Contoh `Register` dengan middleware khusus untuk sebagian route (misalnya auth):

```go
func (h *OrderHandler) Register(r chi.Router) {
    r.Route("/orders", func(r chi.Router) {
        r.Get("/", h.List)          // GET /api/v1/orders
        r.Get("/{id}", h.GetByID)   // ambil id dengan urlParam(r, "id")

        // route di dalam group ini wajib login
        r.Group(func(r chi.Router) {
            r.Use(authMiddleware)
            r.Post("/", h.Create)
            r.Delete("/{id}", h.Delete)
        })
    })
}
```

Middleware bawaan chi (`github.com/go-chi/chi/v5/middleware`, misalnya `Timeout`, `Throttle`, `Compress`) dan middleware `net/http` lain bisa langsung dipakai lewat `r.Use(...)`.

Jadikan modul `products` sebagai contoh, karena sudah memakai semua pola (filter, cache, export/import).

---

## Testing

Unit test (tidak butuh DB):

```bash
make test
```

Test helper Redis memakai Redis asli (otomatis di-skip jika env tidak di-set):

```bash
REDIS_TEST_ADDR=localhost:6379 go test ./pkg/cache/
```

Perintah lain: `make cover` (coverage), `make lint` (`go vet`), `make build` (binary `api`, `migrate`, `seed` ke `bin/`).

---

## Troubleshooting

| Masalah | Solusi |
|---|---|
| `DATABASE_URL is required` | jalankan `cp .env.example .env` lalu isi `DATABASE_URL` |
| `database "go_clean_arch" does not exist` | `createdb -U postgres go_clean_arch` |
| `password authentication failed` | sesuaikan user/password di `DATABASE_URL` |
| `relation "products" does not exist` | `make migrate-up` |
| endpoint file `503 object storage (s3) is not configured` | normal kalau `S3_BUCKET` kosong. Isi config S3 untuk mengaktifkan |
| log `redis unavailable, running without cache` | Redis tidak bisa dihubungi. API tetap jalan tanpa cache |
| presigned URL tidak bisa dibuka dari browser (docker) | set `S3_PRESIGN_ENDPOINT=http://localhost:9000` |
| upload ke presigned URL kena CORS | atur CORS bucket untuk origin FE |
| `413 request body too large` | naikkan `HTTP_MAX_UPLOAD_MB` |
