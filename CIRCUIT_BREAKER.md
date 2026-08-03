# Circuit Breaker — Catatan Kerja

Status per **3 Agustus 2026**: auto-rejection dan breaker per-emiten **aktif**.
Breaker index belum.

Dokumen ini mencatat apa yang sudah dibangun, apa yang belum, dan keputusan
desain yang sudah diambil. Ditulis supaya pekerjaan ini bisa dilanjutkan tanpa
harus membangun ulang konteksnya dari nol.

---

## Ringkas: di mana posisi sekarang

Dua lapisan sudah jalan penuh, dari konfigurasi sampai penegakan:

- **Auto-rejection (ARA/ARB).** Order limit di luar `reference_price ± 30%`
  ditolak 400 dan tidak pernah masuk buku.
- **Circuit breaker per-emiten.** Trade yang tercetak di tepi band menghentikan
  emiten itu selama 2 menit. Halt berakhir sendiri, bertahan melewati restart.

Yang **belum** ada: breaker index 12%. Parameternya tersimpan dan tervalidasi,
tapi belum ada yang membacanya — persis seperti posisi seluruh fitur ini
sebelumnya.

### Kasus yang memicu pekerjaan ini

Harga meloncat 190 → 1000 tanpa hambatan. Sekarang tertutup dua kali:

```
Acuan 190, band 133..247

Order @1000  -> 400 "price 1000 is outside the permitted range 133-247 (reference 190)"
Order @247   -> diterima, trade tercetak
                -> breaker menyala, halt sampai +2 menit
Order @200   -> 400 "trading in AAAA is halted until 2026-08-03T09:02:00Z"
(2 menit kemudian, tanpa request apa pun)
Order @200   -> diterima
```

Diuji di `market/breaker_test.go`, termasuk skenario band berjalan — order
bertahap 190→240→300 tetap ditolak di langkah ketiga karena acuannya tidak ikut
bergerak.

---

## Yang sudah selesai

| Berkas | Isi |
|---|---|
| `migrations/015_circuit_breaker.sql` | Kolom `emiten_halt_bps`, `index_halt_bps`, `halt_duration_seconds` + CHECK, idempoten |
| `marketconfig/ports.go` | Field pada `Settings`, konstanta default dan batas |
| `marketconfig/dto.go` | Field wire pada `ConfigView` dan `UpdateRequest` |
| `marketconfig/service.go` | Penerapan update, validasi, accessor `Halt()` |
| `marketconfig/cache.go` | Seed default, tipe dan accessor `HaltPolicy` |
| `marketconfig/controller.go` | Anotasi swagger |
| `repository/marketconfig.go` | Load dan save kolom baru |
| `marketconfig/service_test.go` | Test — package ini sebelumnya tanpa test sama sekali |

**Penegakan** (tahap kedua):

| Berkas | Isi |
|---|---|
| `migrations/017_reference_price.sql` | Kolom `emiten.reference_price`, tabel `trading_halt` |
| `market/band.go` | `Band` — aritmetika bps, `Allows` vs `AtLimit` |
| `market/registry.go` | State halt di `book`, cek band di `Submit`, `ExpireHalts` |
| `market/directory.go` | Field `Emiten.SessionReference` |
| `breaker/breaker.go` | `Supervisor` — persist halt, sweep yang mengakhirinya |
| `repository/halt.go` | Simpan/hapus/pulihkan halt, set reference price |
| `repository/emiten.go` | Load + set `reference_price` saat aktivasi |
| `order/service.go` | Terjemahan error ke 400 beserta rentang dan waktu resume |
| `marketconfig/cache.go` | Adapter `BreakerPolicy` |
| `main.go` | Wiring, pemulihan halt saat startup, goroutine sweep |
| `market/band_test.go`, `market/breaker_test.go` | Test |

Nilai default sesuai kebijakan bursa ini: emiten **30%** (3000 bps), index
**12%** (1200 bps), durasi halt **2 menit** (120 detik). Ketiganya sudah jadi
default kolom, jadi setelah migration jalan nilainya langsung benar tanpa perlu
memanggil API.

### Dua konvensi yang dipilih dan alasannya

**Persentase disimpan sebagai basis poin (`int64`), bukan float.** 30% → `3000`.
Ambang batas dibandingkan dengan harga rupiah yang sudah `int64` di seluruh
sistem; melibatkan float memunculkan kembali pertanyaan pembulatan tepat di
batas yang menentukan apakah breaker menyala. Pembagiannya `harga * bps / 10000`
— tetap aritmetika bulat. Konstanta pembaginya `marketconfig.BPSDenominator`.

**Durasi disimpan dalam detik di database, `time.Duration` di domain.** Konversi
terjadi sekali saja di repository, bukan di setiap pemanggil. Detik dipilih
ketimbang menit supaya operator bisa memasang durasi di bawah satu menit tanpa
tipe kolomnya memaksa pembulatan.

Validasi membatasi ambang di `1..10000` bps dan durasi di `1..86400` detik.
Batas bawah dan atas ada karena alasan yang berbeda, dan keduanya penting:

- **Nol bukan berarti mati.** Ambang 0 justru memasang breaker yang menyala pada
  transaksi pertama sesi dan menutup pasar. Ini jebakan yang paling mudah
  terjadi lewat JSON yang lupa mengisi field — sebabnya semua field di
  `UpdateRequest` bertipe pointer, supaya "tidak dikirim" dan "dikirim 0" bisa
  dibedakan.
- **Di atas 100% tidak pernah menyala.** Breaker yang tercatat di konfigurasi,
  terbaca sebagai perlindungan di setiap audit, dan tidak melakukan apa pun.
  Lebih buruk daripada tidak ada breaker sama sekali.

---

## Keputusan desain yang sudah diambil

**Acuan band dibekukan per sesi, bukan mengikuti harga terakhir.** Ini yang
paling penting dipahami sebelum mengubah apa pun di sini. Kalau band diukur dari
harga transaksi terakhir, batas 30% membuat 190 mengizinkan 247, yang mengizinkan
321, yang mengizinkan 417 — tiap langkah sah, dan harga sampai 1000 tanpa satu
pun penolakan. Band harus diam supaya yang dibatasi adalah pergerakan kumulatif,
bukan pergerakan per-order.

Karena itu ada dua hal berbeda dengan nama mirip, dan keduanya sengaja dibiarkan
terpisah:

- `market.Emiten.ReferencePrice(lastTrade)` — **valuasi.** Bergerak tiap
  transaksi. Dipakai untuk menilai portofolio.
- `market.Emiten.SessionReference` / kolom `emiten.reference_price` — **jangkar
  band.** Diam sepanjang sesi.

**Tepi band bersifat inklusif, dan `Allows` ≠ `AtLimit`.** Order tepat di
ceiling **diterima dan tereksekusi**; yang terjadi kemudian adalah breaker
menyala. Kalau tepi dibuat menolak, ceiling jadi harga yang tak pernah bisa
tercetak, dan breaker yang menyala saat menyentuhnya tidak akan pernah menyala.
Dua predikat ini beda dan tidak boleh disatukan.

**Halt berakhir berdasarkan jam, bukan berdasarkan timer yang menghapus state.**
`book.halted()` membandingkan deadline dengan waktu sekarang tiap kali dibaca,
jadi emiten terbuka persis saat deadline lewat — terlepas dari apakah sweep sudah
jalan. Sweep hanya mengurus efek samping: hapus baris database dan broadcast.

**Sweep adalah satu poll, bukan satu timer per halt.** Ini konsekuensi langsung
dari model konkurensi di `market/registry.go`: satu mutex mengunci semua buku
supaya matching sekuensial dan deterministik. Goroutine per halt akan membuka
buku dari thread mana pun yang kebetulan dijadwalkan runtime — persis race yang
model itu ada untuk mencegahnya.

**Buku dibekukan, bukan dikosongkan.** Order yang sudah beristirahat tetap ada
beserta prioritas waktunya; hanya order baru yang ditolak.

**Halt bertahan melewati restart.** Tabel `trading_halt` dipulihkan saat startup,
dan halt yang deadline-nya sudah lewat selama proses mati **tidak** dipulihkan —
kalau tidak, halt bisa hidup lebih lama dari durasinya hanya karena servernya
sempat mati.

**`ErrEmitenHalted` ≠ `ErrEmitenInactive`.** Inactive itu status administratif
yang diatur operator; halt itu otomatis, sementara, dan berakhir sendiri. Klien
yang kena halt harus mencoba lagi nanti; yang kena inactive tidak.

---

## Yang belum ada

### Breaker index (12%)

Parameternya tersimpan dan tervalidasi (`index_halt_bps`), tapi belum ada yang
membacanya.

Butuh nilai pembukaan index yang di-snapshot tiap awal sesi sebagai pembanding —
package `index/` belum menyimpannya. Secara arsitektur ini yang paling rumit,
karena satu peristiwa harus menghentikan **semua** emiten serentak. Pola
per-emiten sudah terbukti jalan, jadi bentuknya bisa mengikuti: `Registry` butuh
sesuatu seperti `HaltAll(until)`, dan `index.Notifier` — yang sudah dipanggil
tiap kali harga bergerak — jadi titik deteksinya.

Catatan: breaker index di BEI **hanya satu arah — turun**. Naik ekstrem tidak
menghentikan pasar; euforia beli sudah direm ARA per-saham. Aturan BEI aslinya
berjenjang (5% → 10% → 15%), sedangkan sistem ini memakai satu ambang 12%. Itu
keputusan yang disengaja, bukan penyederhanaan yang terlupa.

### Roll harga acuan antar sesi

Ini **celah yang paling perlu ditutup berikutnya.** `reference_price` sekarang
hanya diisi saat aktivasi emiten (dari harga IPO) dan tidak pernah diperbarui
sesudahnya. Artinya band selamanya diukur dari harga IPO, bukan dari penutupan
kemarin.

Yang dibutuhkan: satu job di batas sesi yang menyetel `reference_price` ke harga
penutupan sesi tersebut. `repository.Halt.SetReferencePrice` dan
`Registry.SetReference` sudah ada — tinggal ada yang memanggilnya.

**Perlu diputuskan:** apa acuan untuk emiten yang tidak diperdagangkan sama
sekali sepanjang sesi? Penutupan sesi terakhir yang ada transaksinya, atau acuan
kemarin dibawa maju apa adanya. Keduanya wajar; yang penting dipilih sadar, bukan
muncul sebagai efek samping dari query.

### Endpoint admin untuk halt manual

`Registry.HaltUntil` dan `Registry.Resume` sudah ada dan bisa dipakai operator
untuk menghentikan atau membuka emiten dengan tangan, tapi belum ada route yang
mengeksposnya.

### Call auction saat pembukaan kembali

Sesuai keputusan, buku dibuka langsung setelah halt berakhir. Konsekuensinya
diterima secara sadar: order yang menumpuk selama halt saling bertabrakan pada
tick pertama. Kalau nanti ini jadi masalah nyata, call auction adalah jawabannya
— dan itu berarti mode pencocokan kedua di engine, berdampingan dengan continuous
matching.

---

## Lapisan pengaman harga lain yang belum ada

Untuk konteks — circuit breaker hanya salah satu lapisan. Diurutkan dari yang
paling sempit:

1. **Tick size / fraksi harga** — harga harus kelipatan tertentu (BEI:
   Rp1/2/5/10/25 tergantung rentang harga). Sudah ada `MinPrice` sebagai lantai,
   tapi aturan kelipatan belum ada.
2. ~~**ARA/ARB**~~ — **sudah ada.** Satu ambang 30% dua arah, bukan berjenjang
   per rentang harga seperti BEI aslinya (35%/25%/20%). Penyederhanaan yang
   disengaja.
3. **Dynamic price collar** — batas ±% dari *harga transaksi terakhir*, bukan
   dari acuan sesi. Menangkap *fat finger* dan flash crash intraday yang lolos
   dari ARA karena pergerakannya bertahap dan masih dalam band. Sekarang murah
   dibangun: `market.Band` sudah ada, tinggal band kedua yang dianchor ke harga
   terakhir dan dicek berdampingan dengan yang pertama. **Rasio manfaat terhadap
   usaha paling tinggi di daftar ini.**
4. **Random closing** — waktu penutupan diacak beberapa detik supaya tidak bisa
   dimanipulasi di detik terakhir.

---

## Urutan yang disarankan

1. **Roll harga acuan antar sesi.** Paling mendesak — tanpa ini band selamanya
   terpatok di harga IPO, dan seluruh lapisan yang sudah dibangun mengukur dari
   angka yang salah begitu emiten diperdagangkan lebih dari satu hari.
2. Endpoint admin untuk halt/resume manual.
3. Snapshot pembukaan index, lalu breaker market-wide 12%.
4. Dynamic collar (lihat daftar lapisan di bawah).
