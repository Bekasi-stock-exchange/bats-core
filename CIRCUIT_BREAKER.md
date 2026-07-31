# Circuit Breaker — Catatan Kerja

Status per **31 Juli 2026**: parameter selesai, penegakan belum ada.

Dokumen ini mencatat apa yang sudah dibangun, apa yang belum, dan keputusan
desain yang harus diambil sebelum melanjutkan. Ditulis supaya pekerjaan ini bisa
dilanjutkan tanpa harus membangun ulang konteksnya dari nol.

---

## Ringkas: di mana posisi sekarang

Konfigurasi circuit breaker sudah lengkap dan bisa diubah lewat
`PUT /api/admin/config`. Nilainya tersimpan, tervalidasi, dan bertahan setelah
restart.

**Tetapi belum ada satu baris pun yang membacanya untuk menghentikan
perdagangan.** `marketconfig.Service.Halt()` ada dan mengembalikan kebijakan yang
berlaku, tapi tidak ada pemanggilnya. `engine.Submit` masih menerima setiap
order tanpa mengecek status apa pun.

Jadi: rem sudah terpasang di dashboard, kabelnya belum tersambung ke roda.

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

## Yang belum ada

Diurutkan berdasarkan ketergantungan — yang di bawah butuh yang di atas.

### 1. Harga acuan per emiten — **prasyarat segalanya**

Pertanyaan "30% dari apa?" belum punya jawaban di sistem ini. Belum ada
`reference_price` pada emiten, dan belum ada yang menyimpan *previous close*
setelah sesi berakhir.

Migration 010 sudah menyimpan harga IPO, dan itu acuan yang benar untuk hari
pertama sebuah emiten diperdagangkan. Tetapi mulai hari kedua acuannya harus
harga penutupan sebelumnya, dan tidak ada apa pun yang mencatatnya sekarang.

Ini juga prasyarat ARA/ARB, jadi mengerjakannya membuka dua hal sekaligus.
Karena itu ini yang paling layak dikerjakan lebih dulu.

**Perlu diputuskan:** apa acuan untuk emiten yang tidak diperdagangkan sama
sekali sepanjang sesi? Penutupan sesi terakhir yang ada transaksinya, atau acuan
kemarin dibawa maju apa adanya. Keduanya wajar; yang penting dipilih sadar,
bukan muncul sebagai efek samping dari query.

### 2. State halt di engine

Halt itu **status**, bukan validasi. Bedanya penting: validasi menolak satu
order dan pasar tetap jalan; status menghentikan seluruh instrumen untuk semua
orang.

`engine.Engine` sekarang tidak punya konsep status sama sekali — `Submit` selalu
mencoba mencocokkan. Perlu ditambahkan status di tingkat emiten (`Active` /
`Halted` / `Suspended`) dan `Submit` harus bisa menolak.

Yang perlu diperhatikan saat mengerjakan: buku order **dibekukan, bukan
dikosongkan**. Order yang sudah beristirahat di buku tetap ada dan tetap
memegang prioritas waktunya; yang ditolak hanya order baru yang masuk. Mengosongkan
buku saat halt akan menghapus prioritas waktu yang justru sedang dilindungi.

Deteksi pemicunya diletakkan setelah transaksi tercetak, bukan saat order masuk —
breaker diukur terhadap transaksi yang benar-benar terjadi, bukan terhadap niat.
`SubmitAtomic` sudah mengembalikan daftar `Trade`, jadi titik sambungnya ada di
sana.

### 3. Komponen berbasis waktu — **perubahan arsitektural**

Halt 2 menit harus berakhir dengan sendirinya. Sistem ini sekarang murni
event-driven: tidak ada satu pun komponen yang berjalan berdasarkan jam.
Menambahkannya bukan sekadar fitur, tapi menambah sumbu baru pada arsitektur.

Perhatikan catatan konkurensi di `engine/matching.go`: pola yang dimaksud adalah
satu channel → satu goroutine → semua order book, supaya matching sekuensial dan
deterministik. Berakhirnya halt **harus masuk lewat jalur yang sama**. Timer yang
membuka buku langsung dari goroutine-nya sendiri akan merusak jaminan itu, dan
kerusakannya berupa race yang muncul sesekali — jenis bug yang paling mahal
dilacak di sistem seperti ini.

### 4. Breaker index (12%)

Butuh nilai pembukaan index yang di-snapshot setiap awal sesi sebagai
pembanding. Package `index/` masih baru; snapshot ini belum ada.

Secara arsitektur ini yang paling rumit, karena satu peristiwa harus
menghentikan **semua** engine serentak dan lintas modul. Kerjakan paling akhir,
setelah pola halt per-emiten terbukti jalan.

Catatan: breaker index di BEI **hanya satu arah — turun**. Naik ekstrem tidak
menghentikan pasar; euforia beli sudah direm ARA per-saham. Aturan BEI aslinya
berjenjang (5% → 10% → 15%), sedangkan sistem ini memakai satu ambang 12%.
Itu keputusan yang disengaja, bukan penyederhanaan yang terlupa.

---

## Keputusan yang harus diambil sebelum menyentuh kode

**Saat halt 2 menit berakhir, buku dibuka bagaimana?**

Dua pilihan:

- **Langsung dibuka.** Sederhana, tapi order yang menumpuk selama halt langsung
  saling bertabrakan pada tick pertama — persis ledakan harga yang membuat
  breaker menyala tadi.
- **Lewat call auction.** Kumpulkan order selama halt, hitung satu harga
  pembukaan yang memaksimalkan volume tereksekusi, lalu buka. Ini yang dipakai
  bursa sungguhan, dan alasannya justru masalah di atas.

Call auction butuh mode pencocokan kedua di engine yang berdampingan dengan
continuous matching yang ada sekarang. **Jauh lebih mahal kalau baru dipikirkan
setelah state halt terlanjur dibangun** — karena itu keputusan ini didahulukan,
bukan ditunda.

---

## Lapisan pengaman harga lain yang belum ada

Untuk konteks — circuit breaker hanya salah satu lapisan. Diurutkan dari yang
paling sempit:

1. **Tick size / fraksi harga** — harga harus kelipatan tertentu (BEI:
   Rp1/2/5/10/25 tergantung rentang harga). Sudah ada `MinPrice` sebagai lantai,
   tapi aturan kelipatan belum ada.
2. **ARA/ARB** — batas ±% dari harga acuan. Preventif, per-order, pasar tetap
   buka. Butuh prasyarat yang sama dengan breaker emiten (bagian 1 di atas).
3. **Dynamic price collar** — batas ±% dari *harga transaksi terakhir*, bukan
   dari previous close. Menangkap *fat finger* dan flash crash intraday yang
   lolos dari ARA karena pergerakannya bertahap. Murah dibangun setelah ARA/ARB
   ada, karena engine sudah tahu harga transaksi terakhir. **Rasio manfaat
   terhadap usaha paling tinggi di daftar ini.**
4. **Random closing** — waktu penutupan diacak beberapa detik supaya tidak bisa
   dimanipulasi di detik terakhir.

---

## Urutan yang disarankan

1. Putuskan pertanyaan call auction di atas.
2. Harga acuan per emiten (membuka ARA/ARB **dan** breaker emiten sekaligus).
3. ARA/ARB — preventif, tidak butuh state, hasil cepat terlihat.
4. State halt per-emiten di engine.
5. Komponen berbasis waktu untuk mengakhiri halt.
6. Dynamic collar.
7. Snapshot pembukaan index, lalu breaker market-wide.
