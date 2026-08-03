# JAST Core

Sebuah **matching engine** minimal yang dimodelkan dari JATS (Jakarta Automated Trading
System) milik Bursa Efek Indonesia. Sistem ini melakukan pencocokan order beli dan jual
secara kontinu dengan prioritas harga-waktu di memori, serta menyimpan data master dan
riwayat transaksi ke PostgreSQL.

Ini adalah **mesin bursa**, bukan aplikasi broker. Sistem hanya mengenal broker
(*partisipan*), bukan investor perorangan, dan tanggung jawabnya berhenti di **eksekusi
transaksi** — kliring (KPEI) dan penyelesaian (KSEI) berada di luar cakupan.

```
Order → [ JATS: matching ] → Trade → [ KPEI: kliring ] → [ KSEI: settlement ]
         ^^^^^^^^^^^^^^^^^^^^^^^^^
         proyek ini
```

## Fitur

- Continuous matching — order dicocokkan begitu masuk
- Tipe order: **limit** dan **market**
- **Prioritas harga-waktu** (FIFO dalam satu level harga)
- Partial fill
- Order book di memori, satu per emiten
- Persistensi PostgreSQL untuk data master dan riwayat transaksi
- REST API untuk mengirim order dan melihat order book
- Streaming perubahan order book via WebSocket
- Autentikasi dua tingkat: admin key statis, dan key per broker yang disimpan ter-hash di database
- Kepemilikan saham per broker, ditulis atomik bersama match yang memindahkannya
- Riwayat harga (eksekusi mentah dan candle OHLC) serta detail instrumen
- Indeks gabungan (IHSG) tertimbang free-float, dengan penyesuaian divisor dan riwayat
- Dokumentasi OpenAPI 3.0 yang digenerate dari kode dan disajikan dari binary

**Sengaja di luar cakupan:** call auction / pra-pembukaan, tipe order lain (stop-loss,
iceberg, FOK, GTD), akun/saldo/portofolio nasabah, kliring & penyelesaian, aksi korporasi,
jam sesi bursa (dan karenanya persentase perubahan indeks), microservice, message broker, dan
frontend apa pun.

## Arsitektur

Monolit modular yang diorganisasi **per domain**, bukan per layer. Setiap domain adalah satu
package yang memuat controller, service, dan transformer-nya sendiri:

| Layer | Bertanggung jawab atas | Tidak pernah melakukan |
|---|---|---|
| **Controller** | Decode request, panggil service, tulis hasil yang sudah ditransformasi | Aturan bisnis, SQL |
| **Service** | Validasi, orkestrasi, batas transaksi | HTTP, string SQL |
| **Transformer** | Tipe domain/engine → DTO JSON | Selain itu |
| **Repository** | Seluruh statement SQL dalam aplikasi | Validasi, matching |

Dependensi mengalir satu arah:

```
order       →  engine, market, platform
orderbook   →  market, platform
market      →  engine
repository  →  order, market        (mengimplementasikan interface yang MEREKA deklarasikan)
main        →  semuanya             (composition root)

engine tidak mengimpor apa pun di luar dirinya
order dan orderbook tidak pernah saling mengimpor
```

`engine` berada di root, bukan di bawah `market`, karena `engine.Order` dan `engine.Trade`
adalah tipe domain yang langsung dipakai package order — ia adalah shared kernel tersendiri,
bukan detail privat milik `market`.

Dua aturan yang menjaga hal itu tetap berlaku:

- **`engine` bersifat murni.** Tanpa library HTTP, tanpa driver database, tanpa
  `encoding/json`, dan tipe-tipenya tidak membawa struct tag — sehingga logika matching
  tetap bisa diekstrak menjadi service tersendiri.
- **Interface dideklarasikan oleh konsumennya.** `order.Repository` dan
  `market.MasterRepository` didefinisikan di package yang memakainya, dan `repository`
  mengimpor package tersebut untuk memenuhinya. Jadi tidak ada package domain yang
  bergantung pada tipe database.

`market` ada supaya kedua domain tidak pernah bersentuhan: ia memiliki order book, satu-satunya
mutex yang menserialisasi matching, direktori data master, dan hub fan-out WebSocket. Hub
membawa `market.BookState` yang bebas tag, itulah sebabnya order service bisa mempublikasikan
pembaruan book tanpa mengimpor DTO milik package orderbook.

`viper` dibatasi hanya di `platform/config`; semua package lain menerima nilai biasa.

### Struktur direktori

```
engine/            order.go  orderbook.go  matching.go  engine_test.go  # inti matching murni
market/            registry.go  directory.go  positions.go  book.go
                   hub.go  ports.go                 # book, LOCK utama, ledger saham, data master
order/             controller.go  service.go  transformer.go  dto.go  ports.go
orderbook/         controller.go  ws_controller.go  service.go  transformer.go  dto.go
participant/       controller.go  service.go  middleware.go  context.go
                   transformer.go  dto.go  ports.go       # identitas broker + auth key
emiten/            controller.go  service.go  transformer.go  dto.go  ports.go
assets/            controller.go  service.go  transformer.go  dto.go  ports.go
trade/             controller.go  service.go  transformer.go  dto.go  ports.go
wallet/            controller.go  service.go  transformer.go  dto.go  ports.go
underwriter/       controller.go  service.go  transformer.go  dto.go  ports.go
marketconfig/      controller.go  service.go  cache.go  dto.go  ports.go  # parameter bursa runtime
index/             controller.go  service.go  cache.go  notifier.go
                   transformer.go  dto.go  ports.go  service_test.go     # indeks gabungan (IHSG)
repository/        repository.go  master.go  emiten.go  participant.go
                   order.go  trade.go  asset.go  wallet.go
                   underwriter.go  marketconfig.go  index.go             # SEMUA SQL di sini
platform/config/   config.go                                            # manajemen env viper
platform/postgres/ pool.go                                              # pool pgx + helper QueryAll
platform/httpx/    respond.go  pagination.go  middleware.go             # JSON, paging, auth
platform/docs/     handler.go  swagger.yaml  swagger.json               # Swagger UI + spec hasil generate
platform/server/   router.go                                            # tabel route
cmd/migrate/       main.go                                              # runner migrasi
cmd/gendocs/       main.go                                              # generasi OpenAPI
migrations/        001_emiten.sql .. 014_index.sql                      # skema + seed
main.go                                                                 # composition root
```

**Semua SQL berada di `repository/`, satu file per fitur.** Tidak ada package lain yang memuat
query. Query order dan trade berada di file terpisah tetapi berbagi satu transaksi, sehingga
hasil matching ditulis secara atomik.

## Kebutuhan

- Go **1.22+** (dikembangkan pada 1.26)
- PostgreSQL (versi terbaru mana pun)

### Dependensi

| Kegunaan | Library |
|---|---|
| HTTP server & routing | `net/http` (stdlib, pola berbasis method) |
| Driver PostgreSQL | `github.com/jackc/pgx/v5` |
| WebSocket | `github.com/coder/websocket` |
| Konfigurasi (env saja) | `github.com/spf13/viper` |
| Logging | `log/slog` (stdlib) |
| Testing | `testing` (stdlib) |
| Generasi spec OpenAPI (CLI build-time) | `github.com/swaggo/swag/v2` |
| Penyajian Swagger UI | `github.com/swaggo/http-swagger/v2` |

## Konfigurasi

Konfigurasi dibaca dari environment (atau file `.env` lokal di root repo). Salin
`.env.example` menjadi `.env` lalu sesuaikan:

| Variabel | Default | Catatan |
|---|---|---|
| `DB_DSN` | — | DSN PostgreSQL. **Wajib** — aplikasi langsung gagal saat startup bila kosong. |
| `API_KEY` | — | Key **admin**, dikirim sebagai `X-API-Key`. **Wajib** — aplikasi langsung gagal bila kosong. Key broker bersifat per-partisipan dan tersimpan di database, bukan di sini. |
| `HTTP_PORT` | `8080` | Port HTTP server |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `DISABLE_DOCS` | `false` | Set `true` untuk menonaktifkan `/docs` dan `/openapi.yaml` |

Nama variabel **tidak** diberi prefix — ejaan yang sama berlaku di shell, di `.env`, dan di
`docker-compose.yml`.

Contoh `DB_DSN`: `postgres://postgres:postgres@localhost:5432/jast?sslmode=disable`

## Memulai

```bash
# 1. Konfigurasi
cp .env.example .env      # lalu isi DB_DSN dan API_KEY (keduanya wajib)

# 2. Buat skema + data seed (idempoten — aman dijalankan ulang)
go run ./cmd/migrate

# 3. Jalankan server
go run .                  # listen di :8080
```

> Gunakan `go run .` (mengompilasi seluruh package), **bukan** `go run main.go`.

Runner migrasi dan server sama-sama mencari `.env` dan `migrations/` dengan menelusuri ke
atas dari working directory, sehingga keduanya bisa dijalankan dari subdirektori mana pun.

### Menggunakan Docker

Seluruh aplikasi — termasuk database PostgreSQL, migrasi otomatis, dan API server — bisa
dijalankan dengan mudah lewat Docker Compose:

```bash
docker compose up --build
```

Setup Docker sudah dikonfigurasi agar **database** dan **app** berjalan otomatis.

Untuk menjalankan migrasi database secara manual lewat Docker:
```bash
docker compose run --rm migrate
```

## API

Dokumentasi interaktif: **http://localhost:8080/docs** (Swagger UI).
Spec mentah: **http://localhost:8080/openapi.yaml**.

Spec **digenerate dari kode** oleh [swag](https://github.com/swaggo/swag) — lihat bagian
[Dokumentasi](#dokumentasi). Hanya dua route dokumentasi tersebut yang tidak memerlukan
autentikasi.

### Dua tingkat autentikasi

| Tingkat | Header | Kredensial | Route |
|---|---|---|---|
| **Admin** | `X-API-Key` | `API_KEY` dari konfigurasi | `/api/admin/*`, `/ws/admin/*` |
| **Partisipan** | `X-Participant-Key` | Key per broker, disimpan ter-hash di database | `/api/participant/*`, `/ws/participant/*` |

Kedua tingkat tidak pernah bercampur: key admin pada route partisipan menghasilkan 401, dan
sebaliknya.

Key broker di-**hash SHA-256**; hanya hash dan prefix pendek non-rahasia yang disimpan. Sebuah
key hanya dikembalikan tepat dua kali sepanjang hidup API — saat broker dibuat, dan saat key
diterbitkan ulang. **Setelah itu tidak bisa diambil lagi**, karena hash tidak bisa dibalik. Key
yang hilang diganti, bukan dipulihkan. Karena itu `GET /api/admin/participants` hanya
menampilkan `api_key_prefix` dan `has_api_key`, tidak pernah key-nya.

Karena autentikasi membaca database pada setiap request, pencabutan key langsung berlaku pada
panggilan berikutnya, bukan menunggu cache kedaluwarsa.

> **Atribusi order ditentukan oleh klien.** `POST /api/participant/orders` tetap menerima
> `participant` di body dan mempercayainya, sehingga broker yang terautentikasi bisa mengirim
> order — dan memindahkan kepemilikan saham — atas nama kode broker lain. Key membuktikan
> bahwa pemanggil adalah *salah satu* broker yang dikenal, bukan *broker yang mana*. Ini
> keputusan yang disengaja, dicatat di sini agar terlihat; identitas terautentikasi tetap
> di-log berdampingan dengan identitas yang diklaim pada setiap submit.

### Onboarding broker

```bash
# Buat broker; key dikembalikan sekali dan hanya sekali.
curl -X POST localhost:8080/api/admin/participants \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' \
  -d '{"kode":"BB","nama":"Broker B"}'
# -> {"kode":"BB","nama":"Broker B","api_key":"jast_BB_9xQ2m..."}

# Terbitkan ulang (membatalkan key lama) atau cabut. Target dikirim lewat body, tidak pernah
# lewat path, sehingga tidak ada identifier yang tercatat di access log.
curl -X POST   localhost:8080/api/admin/participants/apikey \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d '{"participant":"BB"}'
curl -X DELETE localhost:8080/api/admin/participants/apikey \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d '{"participant":"BB"}'
```

### Route partisipan

| Route | Kegunaan |
|---|---|
| `POST /api/participant/orders` | Kirim order |
| `GET /api/participant/orderbook` | Semua book, dengan paginasi |
| `GET /api/participant/orderbook/{kode}` | Satu book, diagregasi per level harga |
| `GET /api/participant/assets` | Kepemilikan saham sendiri, beserta nilai pasar |
| `GET /api/participant/transactions` | Riwayat fill sendiri |
| `GET /api/participant/emiten/{kode}` | Detail instrumen: harga, free float, kapitalisasi pasar |
| `GET /api/participant/emiten/{kode}/prices` | Riwayat harga, eksekusi mentah |
| `GET /api/participant/emiten/{kode}/candles` | Riwayat harga, OHLC (`1m`, `5m`, `1h`, `1d`) |
| `GET /api/participant/index` | Indeks gabungan (IHSG) saat ini |
| `GET /api/participant/index/history` | Riwayat indeks, dengan filter `from`/`to` |
| `GET /ws/participant/orderbook/{kode}` | Stream book (WebSocket) |

```bash
curl -X POST localhost:8080/api/participant/orders \
  -H "X-Participant-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"emiten":"BBCA","participant":"YP","side":"buy","type":"limit","price":8000,"qty":100}'
```

```json
{
  "order":  { "id": 1, "status": "filled", "remaining": 0 },
  "trades": [ { "price": 8000, "qty": 100, "buy_order_id": 1, "sell_order_id": 7 } ]
}
```

`assets` dan `transactions` dibatasi ke pemanggil **berdasarkan key** dan tidak menerima
parameter `participant`, sehingga satu broker tidak bisa membaca posisi atau fill milik broker
lain.

### Route admin

| Route | Kegunaan |
|---|---|
| `GET`/`POST /api/admin/participants` | Daftar atau buat broker |
| `POST`/`DELETE /api/admin/participants/apikey` | Terbitkan atau cabut key broker |
| `GET`/`POST /api/admin/emiten` | Daftar instrumen atau catatkan instrumen baru |
| `GET /api/admin/orders` | Riwayat order |
| `GET /api/admin/trades` | Log eksekusi |
| `GET /api/admin/transactions` | Riwayat fill broker mana pun (`?participant=`) |
| `GET /api/admin/assets` | Kepemilikan lintas broker |
| `GET /api/admin/wallets` | Saldo kas lintas broker |
| `POST /api/admin/wallets` | Tambah atau kurangi saldo kas satu broker |
| `GET`/`POST /api/admin/underwriters` | Daftar atau daftarkan penjamin emisi |
| `POST /api/admin/ipo` | Catatkan instrumen sekaligus bagikan sahamnya |
| `GET`/`PUT /api/admin/config` | Baca atau ubah parameter bursa (`min_price`) |
| `GET /api/admin/index` | Indeks gabungan saat ini |
| `GET /api/admin/index/history` | Riwayat indeks |
| `POST /api/admin/index/recompute` | Hitung ulang indeks sekarang juga |
| `POST /api/admin/index/capture` | Catat titik riwayat indeks sekarang juga |
| `GET /ws/admin/orderbook/{kode}` | Stream book (WebSocket) |

Emiten yang baru dibuat **langsung bisa diperdagangkan** — ia didaftarkan dengan book kosong
di registry yang sedang berjalan, tanpa perlu restart.

### WebSocket

Hanya satu arah (outbound). Saat terkoneksi, server mengirim snapshot penuh, lalu snapshot
baru setiap kali book berubah. Order **tidak pernah** diterima lewat WebSocket. Kedua tingkat
menerima payload identik dari controller yang sama; hanya kredensialnya yang berbeda.

```
ws://localhost:8080/ws/participant/orderbook/BBCA
ws://localhost:8080/ws/admin/orderbook/BBCA
```

Keduanya tidak berada di bawah `/api`.

## Aturan matching

- Order **beli** cocok dengan ask termurah bila `harga_beli >= harga_ask`.
- Order **jual** cocok dengan bid tertinggi bila `harga_jual <= harga_bid`.
- **Harga eksekusi adalah harga order pasif (yang sudah antre)**, bukan harga order yang masuk.
- Pada setiap match, `qty = min(sisa_order_masuk, sisa_order_pasif)`; order pasif yang habis
  terpakai keluar dari book dengan status `filled`.
- Order **limit** dengan sisa kuantitas akan mengantre di book (`open`).
- Order **market** tidak punya batas harga, tidak pernah mengantre di book, dan sisa yang tidak
  terisi akan berstatus `cancelled`.

`Seq` adalah kunci prioritas waktu: monoton dan tidak pernah dipakai ulang. Nilainya diterbitkan
oleh satu sequencer bersama, di-seed saat startup dari nilai tertinggi yang sudah ada di
database, sehingga tetap unik lintas emiten dan lintas restart.

## Kepemilikan saham

`broker_assets_list` mencatat jumlah saham tiap emiten yang dipegang setiap broker. Tabel ini
ditulis **di dalam transaksi yang sama dengan match yang memindahkannya**, sehingga posisi tidak
mungkin berbeda dengan transaksi yang mendasarinya.

Order jual ditolak sebelum matching bila broker tidak punya saham yang cukup. Ketersediaan
dihitung sebagai `kepemilikan − reserved`, di mana *reserved* adalah kuantitas yang sudah
dikomitkan ke order jual broker tersebut yang masih mengantre — tanpa itu, broker dengan 100
lembar bisa mengantrekan dua order jual masing-masing 100 (keduanya lolos pengecekan saldo
naif) lalu menjadi negatif saat keduanya terisi, melanggar `CHECK (amount_shared >= 0)` saat
commit, *setelah* matching terlanjur mengubah book.

Kedua angka tersebut berada di kernel market di bawah **mutex yang sama dengan matching**,
sehingga pengecekan dan komitmen yang diizinkannya menjadi satu langkah atomik. Kepemilikan
di-seed dari database saat startup; reservasi dimulai kosong, konsisten dengan book yang juga
dimulai kosong.

**Nilai pasar bersifat turunan, tidak pernah disimpan.** `nilai = harga_transaksi_terakhir ×
jumlah_lembar`, dihitung saat dibaca, baik untuk kepemilikan maupun kapitalisasi pasar emiten.
Kolom tersimpan harus diperbarui untuk *setiap* pemegang instrumen pada *setiap* transaksi
instrumen tersebut — atau ia diam-diam menjadi usang bagi setiap broker yang tidak
bertransaksi. Nilainya `null`, bukan `0`, untuk instrumen yang belum pernah bertransaksi.

> Book di memori adalah sumber kebenaran untuk matching. Tabel `orders` berperan sebagai
> riwayat/audit; saat restart book dimulai kosong (pemulihan book belum diimplementasikan).

## Konkurensi

Seluruh matching diserialisasi: hanya satu goroutine yang menyentuh order book pada satu waktu,
sehingga matching berjalan sekuensial dan deterministik dan prioritas harga-waktu tidak pernah
dilanggar. Tidak ada locking per order dan tidak ada matching paralel.

Lock berada di dalam `market.Registry` dan bersifat privat. Setiap operasi yang menyentuh book
adalah method pada Registry, sehingga tidak ada pemanggil yang bisa lupa mengambilnya —
`Submit` melakukan matching dan snapshot dalam satu kali akuisisi, yang juga menjamin bahwa
state book yang dikembalikan ke pemanggil persis book yang menghasilkan transaksinya.

## Pengembangan

```bash
make test          # semua test
make test-engine   # test engine saja (tanpa DB) — gerbang paling krusial
make vet
make build
make run           # go run .
make migrate       # go run ./cmd/migrate
make check         # vet + build + test
make docs          # regenerate spec OpenAPI (= go run ./cmd/gendocs)
```

Sebagian setup Windows tidak memiliki `make`; setiap target di atas punya padanan `go` biasa,
dan generasi dokumentasi cukup dengan `go run ./cmd/gendocs`.

Suite test engine (`go test ./engine/...`) mencakup insert terurut, matching
sederhana/parsial/multi-level, order market, tie-break prioritas waktu, dan skenario validasi
acuan — semuanya di memori, tanpa perlu database.

### Dokumentasi

Tidak ada bagian dokumentasi yang ditulis tangan. `platform/docs/swagger.yaml` dan
`swagger.json` **digenerate** dari komentar anotasi pada `main.go` (info umum API) dan pada
method controller, ditambah struct tag pada DTO. UI-nya adalah Swagger UI asli yang dikirim
oleh `swaggo/files` — tidak ada halaman HTML yang dipelihara manual dan tidak ada CDN.

```bash
# sekali saja: install CLI swag (dipin; v2 masih release candidate)
go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5

# setelah mengubah route, DTO, atau anotasi
go run ./cmd/gendocs
```

Commit file hasil regenerate: binary meng-embed `swagger.yaml` dengan `go:embed`, sehingga
build Docker tidak pernah membutuhkan binary swag. Generasi menggunakan `--ot yaml,json`, yang
melewati `docs.go` — swag sendiri hanya alat build-time.

Tiga hal yang perlu diketahui sebelum mengubah setup ini:

- **Spec disajikan sebagai OpenAPI 3.0.3.** swag hanya menghasilkan Swagger 2.0 atau OpenAPI
  3.1 — tidak ada opsi 3.0 — sementara Swagger UI yang dibundel `swaggo/files` tidak bisa
  merender 3.1 (*"does not specify a valid version field"*). Dokumen hasil generate tidak
  memakai konstruk khusus 3.1, jadi `cmd/gendocs` menandainya ulang sebagai 3.0.3, dan
  **gagal dengan lantang** bila suatu saat swag mulai memakai konstruk tersebut, alih-alih
  mengirim spec yang salah menyebut versinya sendiri.
- **Generasi berupa program Go, bukan resep Makefile.** `cmd/gendocs` berjalan di mana pun
  toolchain Go tersedia — tanpa `make`, `bash`, atau `sed`, yang penting di Windows.
- **Swagger UI diarahkan ke `/openapi.yaml`, bukan ke `doc.json` yang diregistrasikan swag.**
  `http-swagger` membaca registry itu lewat swag **v1**, sedangkan spec dihasilkan oleh swag
  **v2** — registry-nya berbeda, sehingga UI akan gagal memuat definisinya. Menyajikan
  dokumen yang di-embed sendiri menghindari ketidakcocokan tersebut.

## Bagaimana sebuah harga terbentuk

Tidak ada kolom "harga saat ini" yang tersimpan. Harga muncul dalam tiga tahap, dan tiap tahap
adalah hal yang berbeda:

1. **Harga order** — ditentukan oleh broker. Order limit membawa batas atas/bawah harganya
   sendiri; order market membawa `0`, artinya "berapa pun yang diberikan book".
2. **Harga eksekusi** — ditentukan oleh matching engine: selalu harga order *pasif* (yang sudah
   mengantre), tidak pernah harga order agresor yang masuk. Itulah imbalan bagi yang lebih dulu
   menyediakan likuiditas, dan itulah sebabnya order beli limit 1250 terhadap ask yang mengantre
   di 1200 tereksekusi pada 1200.
3. **Harga acuan** — nilai yang dipakai untuk *menilai* instrumen:
   `harga transaksi terakhir, atau ipo_price sampai instrumen pertama kali bertransaksi`
   (`market.Emiten.ReferencePrice`). Tidak pernah disimpan, karena satu transaksi saja akan
   membatalkan nilai tersimpan bagi setiap pemegang instrumen tersebut.

Harga IPO menutup celah yang jika tidak akan menjadi lingkaran setan: instrumen yang baru
dicatatkan belum punya transaksi, sehingga tanpa harga IPO `current_price`, kapitalisasi pasar,
dan nilai pasar setiap kepemilikan menjadi null, dan broker pertama yang ingin mengutipnya tidak
punya patokan apa pun. Ini lubang yang sama yang ditutup IDX dengan menjadikan harga penawaran
sebagai previous close pada hari pencatatan.

Keduanya dilaporkan terpisah, bukan digabung. `current_price` (beserta `highest`/`lowest`) tetap
murni berasal dari transaksi dan tetap null sampai pasar benar-benar bersuara; `reference_price`
adalah masukan untuk penilaian, dan `price_source` (`trade` | `ipo`) menyatakan berasal dari
mana nilainya — sehingga klien tidak perlu menebak apakah suatu kapitalisasi pasar mencerminkan
perdagangan nyata.

## Data turunan dari transaksi

Setelah transaksi ada, dua hal mengikuti sebagai turunan (bukan fitur terpisah):

- **Harga saham** = agregasi tabel `trades`. Harga terakhir = harga transaksi terbaru; OHLC =
  agregasi per interval waktu.
- **Indeks (IHSG)** = tertimbang kapitalisasi pasar dari harga-harga saham
  (`market_cap = reference_price × listed_shares`), dibagi divisor dan diskalakan ke basis 100.

### Indeks gabungan

Bobotnya adalah **free-float** — `listed_shares`, bukan `total_shares` — mengikuti metodologi
BEI sejak 2021. Menimbang dengan total saham beredar akan membuat emiten yang mayoritas
sahamnya tidak tercatat mendominasi indeks dengan saham yang tidak bisa dibeli siapa pun.

Setiap instrumen dinilai pada `reference_price`-nya, aturan yang sama dengan endpoint detail
emiten: harga transaksi terakhir, dengan harga IPO sebagai penopang sampai transaksi pertama
terjadi. Instrumen yang tidak punya keduanya **dikeluarkan** dari perhitungan, bukan dihitung
nol — nol berarti "tidak bernilai", pernyataan yang berbeda dan keliru. Karena itu respons
membawa `members` dan `total`: selisih keduanya adalah satu-satunya cara pembaca tahu ada
instrumen yang tidak ikut dihitung.

**Divisor disimpan, tidak pernah dihitung ulang dari anggota yang ada.** Inilah yang membuat
indeks menjadi seri harga, bukan sekadar total kapitalisasi berjalan: saat satu instrumen
dicatatkan, kapitalisasi pasar melonjak sebesar seluruh nilai instrumen itu, padahal tidak ada
satu harga pun yang bergerak. Divisor dinyatakan ulang dengan rasio yang sama sehingga level
indeks tidak berubah melewati peristiwa tersebut, dan setiap pergerakan setelahnya adalah
pergerakan harga yang sungguh-sungguh. Menghitungnya ulang dari nol justru akan menghapus
koreksi itu.

Migrasi `014` menyemai divisor dengan nilai 1 karena file migrasi tidak bisa melihat
kapitalisasi pasar; nilai sebenarnya ditetapkan sekali pada startup pertama. Setelah itu hanya
pencatatan baru yang mengubahnya.

Perhitungan berjalan di goroutine tersendiri, bukan di jalur order. `order.Service` memegang
`submitMu` sepanjang urutan reserve-match-persist, dan menilai seluruh pasar di dalam lock itu
berarti menambahkan biaya valuasi penuh ke latensi setiap order. Sinyalnya satu slot: transaksi
yang datang saat perhitungan masih tertunda tidak memicu perhitungan kedua, sehingga ledakan
eksekusi berbiaya satu kali hitung, bukan satu per eksekusi.

Riwayat disimpan ke `index_snapshot` setiap menit, terpisah dari perhitungannya. Satu baris per
eksekusi hanya akan menggelembungkan tabel tanpa memberi tahu apa pun yang tidak sudah
disampaikan satu titik per menit. Setiap baris menyimpan divisor yang berlaku saat itu, sebab
tanpa itu level lama tidak bisa diverifikasi ulang setelah divisor dinyatakan ulang.

Pembacaan indeks tersedia di **kedua tingkat** dengan payload yang sama persis, karena indeks
adalah satu angka milik seluruh bursa — tidak ada versi per-broker. Handler-nya pun satu,
hanya middleware penjaganya yang berbeda, sehingga kedua view tidak mungkin menyimpang saat
ada field baru. Yang benar-benar khusus admin adalah dua operasinya: `recompute` menilai ulang
seluruh pasar, dan `capture` menulis riwayat — keduanya pekerjaan bursa, bukan sesuatu yang
boleh dijadwalkan sesuka hati oleh satu broker.

Keduanya adalah jalan keluar, bukan bagian dari operasi normal: indeks sudah menghitung ulang
sendiri pada setiap eksekusi dan menyimpan riwayat tiap menit. `recompute` berguna ketika level
menjadi basi bukan karena pasarnya — misalnya pembacaan harga gagal — dan `capture` untuk
menandai momen tertentu tanpa bergantung pada kapan pencatatan periodik kebetulan jatuh.

**Yang belum ada:** persentase perubahan (butuh konsep previous close, yang butuh jam sesi
bursa — keduanya belum ada di sistem), penanganan delisting dan aksi korporasi (jalurnya sendiri
belum ada), serta retensi `index_snapshot`.

## Pasar perdana: penjamin emisi dan IPO

Penjamin emisi (*underwriter*) adalah entitas tersendiri, bukan sekadar flag pada `participant`
— menjamin sebuah penawaran dan berdagang di bursa adalah peran yang berbeda. Meski begitu, ia
tetap menunjuk ke sebuah partisipan, karena saham hanya bermakna bagi pemegang yang bisa
memperdagangkannya: `broker_assets_list`, wallet, dan ledger engine semuanya berkunci pada
partisipan.

Ada dua jenis, dan perbedaannya ditegakkan, bukan sekadar formalitas:

- `utama` — penjamin utama yang menjamin penawaran. Tepat satu per penawaran, dan porsinya
  harus yang **terbesar**.
- `pendukung` — anggota sindikasi pendukung, mengambil porsi lebih kecil. Jumlahnya bebas.

`POST /api/admin/ipo` mencatatkan instrumen sekaligus membagikan sahamnya dalam satu request,
karena instrumen yang sahamnya tidak berada di tangan siapa pun belum bisa disebut penawaran.
Total porsi harus **persis** sama dengan `listed_shares`: kurang berarti ada saham yang tidak
dipegang siapa pun, lebih berarti memunculkan saham yang tidak pernah diterbitkan. Alokasi
berjalan dalam satu transaksi — baris audit `ipo_allocation` dan kredit `broker_assets_list`
bergerak bersama atau tidak sama sekali — dan ledger di memori baru dikredit setelah penulisan
itu berhasil, sebab jika tidak, order jual pertama para penjamin emisi akan ditolak karena
saham dianggap tidak cukup meski database menyatakan sebaliknya.
