package main

import (
	"context"
	"crypto/md5"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	mrand "math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

type StatusEntry struct {
	Number    string
	MessageID string
	Status    string
	Timestamp string
}

var (
	statuses []StatusEntry
	mu       sync.Mutex // Untuk thread-safe write CSV
)

var r *mrand.Rand

func init() {
	r = mrand.New(mrand.NewSource(time.Now().UnixNano()))
}

func main() {
	// Parse command line flags
	logoutFlag := flag.Bool("logout", false, "Log out from current session and delete session data")
	flag.Parse()

	// Setup database untuk session
	var dbLog waLog.Logger
	if os.Getenv("DEBUG") != "" {
		dbLog = waLog.Stdout("Database", "DEBUG", true)
	} else {
		dbLog = waLog.Stdout("Database", "ERROR", false)
	}

	// Buat store
	storeContainer, err := sqlstore.New(context.Background(), "sqlite3", "file:wa_session.db?_foreign_keys=on", dbLog)
	if err != nil {
		panic(fmt.Errorf("gagal membuat store: %v", err))
	}

	// Ambil device
	deviceStore, err := storeContainer.GetFirstDevice(context.Background())
	if err != nil {
		panic(fmt.Errorf("gagal mendapatkan device: %v", err))
	}

	// Setup client
	clientLog := waLog.Stdout("Client", "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	// Channel untuk menandai koneksi berhasil
	connected := make(chan struct{})

	// Handle logout flag
	if *logoutFlag {
		fmt.Println("Logging out and deleting session data...")
		err := client.Logout(context.Background())
		if err != nil {
			fmt.Println("Error logging out:", err)
		} else {
			fmt.Println("Successfully logged out. Session data deleted.")
		}
		return
	}

	// Event handler untuk QR dan status pesan
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected, *events.PushNameSetting:
			if len(client.Store.PushName) > 0 && client.IsConnected() {
				// Login sukses
				fmt.Println("Login event: success")
				// Tandai koneksi berhasil
				select {
				case <-connected: // Channel sudah ditutup
				default:
					close(connected)
				}
			}
		case *events.LoggedOut:
			fmt.Println("\nYou have been logged out from another device.")
			fmt.Println("Please restart the application to generate a new QR code.")
			os.Exit(0)
		case *events.Receipt:
			// Tangani status pengiriman
			var status string
			switch v.Type {
			case types.ReceiptTypeDelivered:
				status = "delivered"
			case types.ReceiptTypeRead:
				status = "read"
			default:
				return
			}
			updateStatus(v.Sender.User, v.MessageIDs[0], status)
		}
	})

	// Jika belum paired, generate QR
	if client.Store.ID == nil {
		// Generate QR code untuk login
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			panic(err)
		}

		// Tampilkan QR code di terminal
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("\nScan this QR code with WhatsApp on your phone:")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else if evt.Event == "timeout" {
				fmt.Println("\nQR code expired. Please restart the application to generate a new one.")
				os.Exit(0)
			} else if evt.Event == "success" {
				fmt.Println("\nSuccessfully paired!")
				break
			}
		}
	} else {
		err = client.Connect()
		if err != nil {
			panic(err)
		}
		// Jika sudah ada sesi, langsung tandai terhubung
		if client.IsConnected() {
			select {
			case <-connected: // Channel sudah ditutup
			default:
				close(connected)
			}
		}
	}

	// Tunggu hingga koneksi siap
	fmt.Println("Menunggu koneksi siap...")
	select {
	case <-connected:
		fmt.Println("Koneksi WhatsApp siap")
	case <-time.After(30 * time.Second):
		if !client.IsConnected() {
			fmt.Println("Gagal menyambung ke WhatsApp dalam waktu yang ditentukan")
			return
		}
		// Jika sudah terhubung tapi channel belum tertutup, lanjutkan saja
		fmt.Println("Terhubung ke WhatsApp")
	}

	// Inisialisasi file status.csv
	err = initStatusCSV("status.csv")
	if err != nil {
		fmt.Println("Error inisialisasi status.csv:", err)
		return
	}

	// Baca file CSV nomor
	numbers, err := readCSV("numbers.csv")
	if err != nil {
		fmt.Println("Error baca CSV:", err)
		return
	}

	// Path ke file media (ganti sesuai kebutuhan)
	mediaPath := "" // atau "path/to/your/video.mp4"
	prefix := `Yang Terkasih Bp/Ibu/Sdr/Sdri
*%s*

Salam Damai Kristus,

Puji Tuhan, atas penyertaan Tuhan, proses Pemanggilan Pendeta Kedua GKJ Pamulang akan segera memasuki tahap pemilihan. Untuk itu kami Majelis dan Panitia Pemanggilan Pendeta Kedua GKJ Pamulang, mengundang Bapak/Ibu/Sdr/Sdri, jemaat dewasa GKJ  Pamulang (sudah sidi / baptis dewasa) untuk hadir dalam pemilihan pendeta yang akan dilaksanakan pada :

*Hari : Minggu*
*Tanggal : 28 September 2025*
*Waktu : Jam 10.00 WIB (Setelah Ibadah Minggu)*
*Tempat : GKJ Pamulang*

Namun demikian, apabila Bapak/Ibu/Sdr/Sdri, tidak dapat hadir di gereja pada hari Minggu, 28 September 2025, *dapat melakukan pemilihan secara online dengan tata cara sebagai berikut* :
1. Mendaftar pemilihan secara online dengan mengirim pesan Whatsapp ke nomor +62-857-1871-2605 dan mengikuti petunjuk sesuai balasan yang di berikan dari nomor Whatsapp tersebut
2. Pendaftaran pemilihan online paling lambat 21 september 2025
3. Setelah pendaftaran online berhasil, Bapak/Ibu/Sdr/Sdri, akan mendapatkan link untuk mengikuti pemilihan online
4. Pemilihan online di buka mulai Tanggal 25 September 2025 Jam 00:00 WIB sampai dengan Tanggal 27 September 2025 Jam 24:00 WIB

Suara Bapak/Ibu/Sdr/Sdri, sangat berarti untuk kesuksesan pelaksanaan Pemilihan Pendeta dan masa depan GKJ Pamulang.

*Tuhan memberkati*

*Panitia Pemilihan Pendeta GKJ Pamulang*
`

	// Generate MD5 hash of current timestamp
	hash := md5.Sum([]byte(fmt.Sprint(time.Now().UnixMilli())))
	var caption string

	// Tunggu sebentar untuk memastikan koneksi stabil
	fmt.Println("Menunggu koneksi stabil...")
	time.Sleep(3 * time.Second)

	if !client.IsConnected() {
		fmt.Println("Kesalahan: Tidak terhubung ke WhatsApp. Silakan coba lagi.")
		return
	}

	// Cek dan kirim media ke setiap nomor
	for num, name := range numbers {
		fmt.Printf("\nMemproses nomor %s : %s\n", num, name)
		caption = fmt.Sprintf(prefix, name, hex.EncodeToString(hash[:]))
		// Periksa format nomor
		//if !strings.HasPrefix(num, "+") && !strings.HasPrefix(num, "62") {
		//	num = "62" + strings.TrimLeft(num, "0")
		//}

		jid := parseJID(num)
		if jid.User == "" {
			fmt.Printf("Nomor invalid: %s\n", num)
			updateStatus(num, "", "invalid_number")
			continue
		}

		// Cek apakah nomor aktif di WhatsApp
		fmt.Printf("Memeriksa nomor WhatsApp: %s\n", num)
		resp, err := client.IsOnWhatsApp([]string{num})
		if err != nil {
			fmt.Printf("Gagal memeriksa nomor %s: %v\n", num, err)
			updateStatus(num, "", "check_failed")
			continue
		}

		if len(resp) == 0 || !resp[0].IsIn {
			fmt.Printf("Nomor %s tidak terdaftar di WhatsApp\n", num)
			updateStatus(num, "", "not_on_whatsapp")
			continue
		}

		// Jika nomor valid, lanjutkan dengan pengiriman pesan
		fmt.Printf("Mengirim pesan ke %s...\n", num)
		msgID, err := sendMedia(client, jid, mediaPath, caption)
		if err != nil {
			fmt.Printf("Gagal mengirim pesan ke %s: %v\n", num, err)
			updateStatus(num, "", "send_failed")
		} else {
			fmt.Printf("Pesan terkirim ke %s, MessageID: %s\n", num, msgID)
			updateStatus(num, msgID, "sent")
		}

		// Delay acak 10-60 detik
		time.Sleep(time.Duration(60+r.Intn(120)) * time.Second)
	}

	// Handle graceful shutdown
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	// Tunggu pesan selesai terkirim atau ada interupsi
	select {
	case <-ch:
		fmt.Println("\nMenerima sinyal interupsi, memutuskan koneksi...")
	case <-time.After(5 * time.Minute): // Timeout 5 menit untuk mengirim pesan
		fmt.Println("\nWaktu pengiriman pesan habis")
	}

	// Tunggu sebentar sebelum memutuskan koneksi
	time.Sleep(2 * time.Second)
	if client.IsConnected() {
		client.Disconnect()
	}
}

// Fungsi parse JID
func parseJID(arg string) types.JID {
	if !strings.ContainsRune(arg, '@') {
		return types.NewJID(arg, types.DefaultUserServer)
	}
	jid, err := types.ParseJID(arg)
	if err != nil {
		return types.JID{}
	}
	return jid
}

// Fungsi baca CSV
func readCSV(filename string) (map[string]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka file %s: %v", filename, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("gagal membaca file CSV: %v", err)
	}

	numbers := make(map[string]string)
	for _, record := range records {
		if len(record) > 0 {
			number := strings.TrimSpace(record[0])
			if number != "" {
				numbers[number] = record[1]
			}
		}
	}

	if len(numbers) == 0 {
		return nil, fmt.Errorf("file CSV kosong atau tidak berisi nomor yang valid")
	}

	return numbers, nil
}

// Fungsi kirim media (mengembalikan MessageID)
// Jika mediaPath kosong, hanya mengirim pesan teks
func sendMedia(client *whatsmeow.Client, jid types.JID, mediaPath, caption string) (string, error) {
	// Jika mediaPath kosong, kirim pesan teks saja
	if mediaPath == "" {
		if caption == "" {
			return "", fmt.Errorf("pesan tidak boleh kosong ketika tidak ada media")
		}

		// Kirim pesan teks
		resp, err := client.SendMessage(context.Background(), jid, &waProto.Message{
			Conversation: proto.String(caption),
		})
		if err != nil {
			return "", fmt.Errorf("gagal mengirim pesan teks: %v", err)
		}
		return resp.ID, nil
	}

	// Baca file media
	data, err := os.ReadFile(mediaPath)
	if err != nil {
		return "", fmt.Errorf("gagal baca file: %v", err)
	}

	// Tentukan tipe media
	ext := strings.ToLower(filepath.Ext(mediaPath))
	var msg *waProto.Message
	var mimeType string

	switch ext {
	case ".jpg", ".jpeg", ".png":
		mimeType = "image/jpeg"
		if ext == ".png" {
			mimeType = "image/png"
		}
		uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaImage)
		if err != nil {
			return "", fmt.Errorf("gagal upload gambar: %v", err)
		}
		msg = &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				Caption:       proto.String(caption),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String(mimeType),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	case ".mp4":
		mimeType = "video/mp4"
		uploaded, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
		if err != nil {
			return "", fmt.Errorf("gagal upload video: %v", err)
		}
		msg = &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				Caption:       proto.String(caption),
				URL:           proto.String(uploaded.URL),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				Mimetype:      proto.String(mimeType),
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(data))),
			},
		}
	default:
		return "", fmt.Errorf("format file tidak didukung: %s", ext)
	}

	// Kirim pesan dan dapatkan MessageID
	resp, err := client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// Inisialisasi file status.csv
func initStatusCSV(filename string) error {
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("gagal membuat/membuka file %s: %v", filename, err)
	}
	defer file.Close()

	// Cek apakah file kosong untuk menulis header
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("gagal mendapatkan info file: %v", err)
	}

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Tulis header hanya jika file baru
	if fileInfo.Size() == 0 {
		if err := writer.Write([]string{"Number", "MessageID", "Status", "Timestamp"}); err != nil {
			return fmt.Errorf("gagal menulis header ke file CSV: %v", err)
		}
	}

	return nil
}

// Update status dan tulis ke CSV
func updateStatus(number, messageID, status string) {
	mu.Lock()
	defer mu.Unlock()

	// Tambah atau update status
	found := false
	for i, entry := range statuses {
		if entry.Number == number && entry.MessageID == messageID {
			statuses[i].Status = status
			statuses[i].Timestamp = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}
	if !found {
		statuses = append(statuses, StatusEntry{
			Number:    number,
			MessageID: messageID,
			Status:    status,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	// Tulis ulang CSV
	file, err := os.Create("status.csv")
	if err != nil {
		fmt.Println("Error menulis status.csv:", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Tulis header
	writer.Write([]string{"Number", "MessageID", "Status", "Timestamp"})
	// Tulis semua status
	for _, entry := range statuses {
		writer.Write([]string{entry.Number, entry.MessageID, entry.Status, entry.Timestamp})
	}
}
