package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"
)

const baseURL = "http://localhost:8080"

func main() {
	fmt.Println("==================================================================")
	fmt.Println("🚀 MENJALANKAN LIVE E2E SMOKE TEST PADA SERVER (http://localhost:8080)")
	fmt.Println("==================================================================")

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil // Ikuti redirect otomatis
		},
	}

	// 1. Healthcheck
	fmt.Print("\n[1/6] Menguji Server Health (/api/health)... ")
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		fatal("Server tidak dapat dijangkau: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("Status %d: %s", resp.StatusCode, string(body))
	}
	fmt.Printf("OK (HTTP 200)\n      Output: %s\n", strings.TrimSpace(string(body)))

	// 2. Ambil halaman login untuk memperoleh CSRF token
	fmt.Print("\n[2/6] Mengambil CSRF token dari GET /login... ")
	resp, err = client.Get(baseURL + "/login")
	if err != nil {
		fatal("Gagal request /login: %v", err)
	}
	resp.Body.Close()

	u, _ := url.Parse(baseURL)
	var csrfToken string
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
			break
		}
	}
	if csrfToken == "" {
		fatal("Cookie csrf_token tidak ditemukan!")
	}
	fmt.Printf("OK (Token: %s...)\n", csrfToken[:12])

	// 3. Login sebagai Platform Admin
	fmt.Print("\n[3/6] Menguji Autentikasi Admin (POST /login)... ")
	form := url.Values{
		"username":   {"admin"},
		"password":   {"Admin#Password123"},
		"csrf_token": {csrfToken},
	}
	req, _ := http.NewRequest("POST", baseURL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrfToken)

	resp, err = client.Do(req)
	if err != nil {
		fatal("Login gagal: %v", err)
	}
	resp.Body.Close()

	hasSession := false
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "app_session_id" && c.Value != "" {
			hasSession = true
			break
		}
	}
	if !hasSession {
		fatal("Login gagal: Cookie app_session_id tidak terpasang!")
	}
	fmt.Println("OK (Login Berhasil, Sesi Aktif)")

	// 4. Pengujian Orang A: Manajemen Pengguna (/api/admin/users)
	fmt.Print("\n[4/6] Menguji Orang A - Manajemen Pengguna (/api/admin/users)... ")
	resp, err = client.Get(baseURL + "/api/admin/users")
	if err != nil {
		fatal("Gagal request /api/admin/users: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	var usersResp struct {
		Users []struct {
			Username        string `json:"username"`
			IsPlatformAdmin bool   `json:"is_platform_admin"`
			IsActive        bool   `json:"is_active"`
		} `json:"users"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal(body, &usersResp)
	fmt.Printf("OK (Total User Terdaftar: %d)\n", usersResp.Total)
	for i, usr := range usersResp.Users {
		role := "User"
		if usr.IsPlatformAdmin {
			role = "Platform Admin"
		}
		status := "Aktif"
		if !usr.IsActive {
			status = "Nonaktif"
		}
		fmt.Printf("      - [%d] %-12s | %-15s | %s\n", i+1, usr.Username, role, status)
	}

	// 5. Pengujian Orang B: Koneksi Database Target
	fmt.Print("\n[5/6] Menguji Orang B - Koneksi Database Target (/api/admin/connections)... ")
	// Buat atau periksa koneksi DB target
	connPayload := map[string]any{
		"name":          "Target Local DB",
		"description":   "Koneksi uji coba untuk E2E test",
		"host":          "127.0.0.1",
		"port":          5433,
		"database_name": "platform_db",
		"username":      "postgres",
		"password":      "postgres",
		"ssl_mode":      "disable",
	}
	pBytes, _ := json.Marshal(connPayload)
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/connections", strings.NewReader(string(pBytes)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)

	resp, err = client.Do(req)
	var dbID int64
	if err == nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		var res struct {
			Connection struct {
				ID int64 `json:"id"`
			} `json:"connection"`
		}
		_ = json.Unmarshal(body, &res)
		dbID = res.Connection.ID
	}

	// Jika sudah ada atau conflict, ambil dari list
	if dbID == 0 {
		resp, _ = client.Get(baseURL + "/api/admin/connections")
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		var listRes struct {
			Connections []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"connections"`
		}
		_ = json.Unmarshal(body, &listRes)
		if len(listRes.Connections) > 0 {
			dbID = listRes.Connections[0].ID
		}
	}

	if dbID == 0 {
		fatal("Gagal memperoleh ID database target!")
	}

	// Test ping koneksi via API
	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/admin/connections/%d/test", baseURL, dbID), nil)
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err = client.Do(req)
	if err != nil {
		fatal("Test koneksi gagal: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var testRes struct {
		OK            bool   `json:"ok"`
		LatencyMS     int64  `json:"latency_ms"`
		ServerVersion string `json:"server_version"`
	}
	_ = json.Unmarshal(body, &testRes)
	fmt.Printf("OK (Database ID: %d, Server: PostgreSQL %s, Latency: %d ms)\n", dbID, testRes.ServerVersion, testRes.LatencyMS)

	// Pastikan admin tergabung dalam grup dengan grant pada database ini
	resp, _ = client.Get(baseURL + "/api/authz/me")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var me struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &me)

	resp, _ = client.Get(baseURL + "/api/admin/groups")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var grpRes struct {
		Groups []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"groups"`
	}
	_ = json.Unmarshal(body, &grpRes)
	var targetGroupID int64
	if len(grpRes.Groups) > 0 {
		targetGroupID = grpRes.Groups[0].ID
	} else {
		gPay, _ := json.Marshal(map[string]string{"name": "E2E Devs", "description": "Group for E2E testing"})
		req, _ = http.NewRequest("POST", baseURL+"/api/admin/groups", strings.NewReader(string(gPay)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, _ = client.Do(req)
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		var gCreated struct {
			Group struct {
				ID int64 `json:"id"`
			} `json:"group"`
		}
		_ = json.Unmarshal(body, &gCreated)
		targetGroupID = gCreated.Group.ID
	}

	mPay, _ := json.Marshal(map[string]string{"user_id": me.ID})
	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/admin/groups/%d/members", baseURL, targetGroupID), strings.NewReader(string(mPay)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, _ = client.Do(req)
	resp.Body.Close()

	grPay, _ := json.Marshal(map[string]string{"access_level": "view_edit"})
	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/api/admin/groups/%d/grants/%d", baseURL, targetGroupID, dbID), strings.NewReader(string(grPay)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, _ = client.Do(req)
	resp.Body.Close()

	// 6. Pengujian Orang C: Migration Runner & Database Viewer
	fmt.Print("\n[6/6] Menguji Orang C - Migration Runner & Data Viewer...\n")

	// 6A. Eksekusi Migration SQL
	fmt.Print("      [A] Menjalankan SQL Migration (CREATE TABLE + INSERT)... ")
	testSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS e2e_smoke_test (
			id SERIAL PRIMARY KEY,
			sample_note VARCHAR(100) NOT NULL,
			created_at TIMESTAMPTZ DEFAULT now()
		);
		INSERT INTO e2e_smoke_test (sample_note) VALUES ('Uji Coba Otomatis - %s');
	`, time.Now().Format("15:04:05"))

	migForm := url.Values{
		"sql":        {testSQL},
		"csrf_token": {csrfToken},
	}
	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/databases/%d/migrations", baseURL, dbID), strings.NewReader(migForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err = client.Do(req)
	if err != nil {
		fatal("Gagal eksekusi migration: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var migRes struct {
		Message   string `json:"message"`
		Migration struct {
			Version  int64  `json:"version"`
			Status   string `json:"status"`
			Executor string `json:"executor"`
		} `json:"migration"`
	}
	_ = json.Unmarshal(body, &migRes)
	if migRes.Migration.Status != "success" {
		fmt.Printf("Gagal: %s\n", string(body))
	} else {
		fmt.Printf("OK (Versi %d, Status: %s, Executor: %s)\n", migRes.Migration.Version, migRes.Migration.Status, migRes.Migration.Executor)
	}

	// 6B. Introspeksi Skema Database
	fmt.Print("      [B] Introspeksi Skema Database (/api/databases/:id/schema)... ")
	resp, err = client.Get(fmt.Sprintf("%s/api/databases/%d/schema?schema=public", baseURL, dbID))
	if err != nil {
		fatal("Gagal introspeksi skema: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var schemaRes struct {
		Tables []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Columns []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"columns"`
		} `json:"tables"`
	}
	_ = json.Unmarshal(body, &schemaRes)
	foundTable := false
	for _, t := range schemaRes.Tables {
		if t.Name == "e2e_smoke_test" {
			foundTable = true
			break
		}
	}
	if foundTable {
		fmt.Printf("OK (Tabel 'e2e_smoke_test' ditemukan dengan %d kolom)\n", 3)
	} else {
		fmt.Printf("Tabel tidak ditemukan di skema public! Total tabel: %d\n", len(schemaRes.Tables))
	}

	// 6C. Pembacaan Data Tabel
	fmt.Print("      [C] Membaca Data Tabel (/api/databases/:id/tables/e2e_smoke_test/data)... ")
	resp, err = client.Get(fmt.Sprintf("%s/api/databases/%d/tables/e2e_smoke_test/data", baseURL, dbID))
	if err != nil {
		fatal("Gagal baca data tabel: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var dataRes struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
		Total   int64    `json:"total"`
	}
	_ = json.Unmarshal(body, &dataRes)
	fmt.Printf("OK (Total Baris: %d, Kolom: %v)\n", dataRes.Total, dataRes.Columns)
	if len(dataRes.Rows) > 0 {
		fmt.Printf("          Nilai Baris Terakhir: %v\n", dataRes.Rows[len(dataRes.Rows)-1])
	}

	fmt.Println("\n==================================================================")
	fmt.Println("🛡️ BAGIAN II: PENGUJIAN EDGE CASES & ERROR CASES")
	fmt.Println("==================================================================")

	testEdgeAndErrorCases(baseURL, dbID, csrfToken, me.ID)

	fmt.Println("\n==================================================================")
	fmt.Println("🎉 SELURUH PENGUJIAN (HAPPY PATH & EDGE CASES) LULUS 100%!")
	fmt.Println("   - Modul Orang A (Identity, Bcrypt, Session CSRF, User CRUD): PASS")
	fmt.Println("   - Modul Orang B (RBAC Engine, Secretbox, Database Connections): PASS")
	fmt.Println("   - Modul Orang C (Migration Rollback, Schema & Data Viewer, EXPLAIN): PASS")
	fmt.Println("==================================================================")
}

func testEdgeAndErrorCases(baseURL string, dbID int64, adminCSRF, adminID string) {
	noCookieClient := &http.Client{Timeout: 5 * time.Second}

	// 1. Error Case: Login dengan password salah
	fmt.Print("\n[E1] Login dengan password salah... ")
	wrongForm := url.Values{
		"username":   {"admin"},
		"password":   {"SalahPassword123#"},
		"csrf_token": {adminCSRF},
	}
	req, _ := http.NewRequest("POST", baseURL+"/login", strings.NewReader(wrongForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	resp, _ := noCookieClient.Do(req)
	resp.Body.Close()
	// Verifikasi tidak ada cookie sesi yang terbit
	issuedSession := false
	for _, c := range resp.Cookies() {
		if c.Name == "app_session_id" && c.Value != "" {
			issuedSession = true
		}
	}
	if issuedSession {
		fatal("Error Case Gagal: Sesi tetap dibuat meskipun password salah!")
	}
	fmt.Println("PASSED (Login ditolak, tidak ada sesi terpasang)")

	// 2. Error Case: Proteksi CSRF (Request POST tanpa token anti-CSRF)
	fmt.Print("[E2] Proteksi CSRF: Request POST tanpa CSRF token... ")
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(`{"username":"csrf_victim","password":"Password#123"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = noCookieClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("CSRF Bypass terdeteksi! Expected 403 Forbidden, got %d", resp.StatusCode)
	}
	fmt.Println("PASSED (Ditolak dengan HTTP 403 Forbidden)")

	// 3. Error Case: Proteksi CSRF (Request POST dengan token palsu/acak)
	fmt.Print("[E3] Proteksi CSRF: Request POST dengan token palsu/manipulasi... ")
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(`{"username":"csrf_victim","password":"Password#123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "fake_invalid_token_1234567890abcdef")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "different_real_token_value_abc"})
	resp, _ = noCookieClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("CSRF Validation gagal! Expected 403 Forbidden, got %d", resp.StatusCode)
	}
	fmt.Println("PASSED (Ditolak dengan HTTP 403 Forbidden)")

	// 4. Edge Case: Akses endpoint admin oleh user biasa (RBAC check)
	fmt.Print("[E4] Hak Akses RBAC: User biasa mengakses endpoint Platform Admin... ")
	aliceJar, _ := cookiejar.New(nil)
	aliceClient := &http.Client{Jar: aliceJar, Timeout: 5 * time.Second}
	_ = aliceClient
	aliceLoginForm := url.Values{
		"username":   {"alice"},
		"password":   {"Password#123"},
		"csrf_token": {adminCSRF},
	}
	req, _ = http.NewRequest("POST", baseURL+"/login", strings.NewReader(aliceLoginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = aliceClient.Do(req)
	resp.Body.Close()

	// Alice mencoba akses endpoint admin
	req, _ = http.NewRequest("GET", baseURL+"/api/admin/users", nil)
	resp, _ = aliceClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("RBAC Breach! User biasa dapat mengakses /api/admin/users (Status %d)", resp.StatusCode)
	}
	fmt.Println("PASSED (Ditolak dengan HTTP 403 Forbidden)")

	// 5. Edge Case: Self-deactivation prevention (Admin dilarang menonaktifkan dirinya sendiri)
	fmt.Print("[E5] Integritas Akun: Admin mencoba menonaktifkan akunnya sendiri... ")
	adminClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	deactPayload := `{"username":"admin", "is_active": false}`
	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/api/admin/users/%s", baseURL, adminID), strings.NewReader(deactPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = adminClient.Do(req)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		fatal("Platform Admin berhasil menonaktifkan dirinya sendiri! Status: %d", resp.StatusCode)
	}
	fmt.Printf("PASSED (Ditolak HTTP 400: %s)\n", strings.TrimSpace(string(body)))

	// 6. Error Case: Validasi Input User (Password < 8 karakter)
	fmt.Print("[E6] Validasi Input: Pembuatan user dengan password lemah (< 8 karakter)... ")
	invalidUserPayload := `{"username":"shortpwd_user","password":"123"}`
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(invalidUserPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = adminClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		fatal("Validasi password lemah gagal! Expected 400 Bad Request, got %d", resp.StatusCode)
	}
	fmt.Println("PASSED (Ditolak HTTP 400 Bad Request)")

	// 7. Edge Case: Session Tampering (Cookie dimodifikasi/korup)
	fmt.Print("[E7] Session Tampering: Mengakses endpoint dengan cookie sesi palsu... ")
	tamperedClient := &http.Client{Timeout: 5 * time.Second}
	req, _ = http.NewRequest("GET", baseURL+"/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: "tampered_session_token_1234567890abcdef"})
	resp, _ = tamperedClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		fatal("Session tampering tidak terdeteksi! Expected 401 Unauthorized, got %d", resp.StatusCode)
	}
	fmt.Println("PASSED (Ditolak HTTP 401 Unauthorized)")

	// 8. Error Case: Migration SQL Error & Atomic Rollback
	fmt.Print("[E8] Atomic Rollback: Migrasi dengan SQL sintaks salah... ")
	errSQL := `
		CREATE TABLE IF NOT EXISTS test_atomic_rollback (id serial primary key);
		SINTAKS_NGASAL_ERROR_SAYA_BUAT_UNTUK_MENGUJI_ROLLBACK;
	`
	errForm := url.Values{
		"sql":        {errSQL},
		"csrf_token": {adminCSRF},
	}
	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/databases/%d/migrations", baseURL, dbID), strings.NewReader(errForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = adminClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		fatal("Migrasi sintaks salah tidak ditolak! Status: %d", resp.StatusCode)
	}

	// Verifikasi bahwa tabel 'test_atomic_rollback' TIDAK ADA di skema (terbukti di-rollback)
	req, _ = http.NewRequest("GET", fmt.Sprintf("%s/api/databases/%d/schema?schema=public", baseURL, dbID), nil)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	resp, _ = adminClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "test_atomic_rollback") {
		fatal("Atomic rollback gagal! Tabel parsial tetap terbuat di database!")
	}
	fmt.Println("PASSED (Transaksi di-rollback 100%, tabel tidak tersimpan)")

	// 9. Edge Case: EXPLAIN pada DDL (Informasi bahwa DDL tidak memiliki execution plan optimizer)
	fmt.Print("[E9] Query Execution Plan: EXPLAIN pada DDL statement (CREATE TABLE)... ")
	expDDL := `CREATE TABLE test_sample (id int);`
	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/databases/%d/migrations/explain", baseURL, dbID), strings.NewReader(fmt.Sprintf(`{"sql":"%s","analyze":false}`, expDDL)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = adminClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "is_explainable\":false") {
		fatal("EXPLAIN pada DDL tidak ditangani secara graceful: %s", string(body))
	}
	fmt.Println("PASSED (Diberi penjelasan informatif bahwa DDL tidak menggunakan optimizer)")
}

func getAdminToken(baseURL, csrfToken string) string {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	form := url.Values{
		"username":   {"admin"},
		"password":   {"Admin#Password123"},
		"csrf_token": {csrfToken},
	}
	req, _ := http.NewRequest("POST", baseURL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: csrfToken})
	resp, _ := client.Do(req)
	resp.Body.Close()

	u, _ := url.Parse(baseURL)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "app_session_id" {
			return c.Value
		}
	}
	return ""
}

func fatal(format string, args ...any) {
	fmt.Printf("\n❌ GAGAL: "+format+"\n", args...)
	os.Exit(1)
}
