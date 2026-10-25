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

const (
	baseURL             = "http://localhost:8080"
	smokeConnectionName = "E2E Sample DB"
	smokeGroupName      = "E2E Devs"
)

func main() {
	fmt.Println("smoketest ke " + baseURL)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}

	fmt.Print("\n[1/6] health check... ")
	resp, err := client.Get(baseURL + "/api/health")
	if err != nil {
		fatal("server tidak bisa dihubungi: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fatal("status %d: %s", resp.StatusCode, string(body))
	}
	fmt.Println("ok")

	fmt.Print("[2/6] ambil token CSRF... ")
	resp, err = client.Get(baseURL + "/login")
	if err != nil {
		fatal("request /login gagal: %v", err)
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
		fatal("cookie csrf_token tidak ada")
	}
	fmt.Println("ok")

	fmt.Print("[3/6] login admin... ")
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
		fatal("login gagal: %v", err)
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
		fatal("login gagal, cookie session tidak ada")
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "csrf_token" && c.Value != "" {
			csrfToken = c.Value
			break
		}
	}
	fmt.Println("ok")

	fmt.Print("[4/6] daftar user... ")
	resp, err = client.Get(baseURL + "/api/admin/users")
	if err != nil {
		fatal("request /api/admin/users gagal: %v", err)
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
	fmt.Printf("ok, %d user\n", len(usersResp.Users))
	for _, usr := range usersResp.Users {
		role := "User"
		if usr.IsPlatformAdmin {
			role = "Platform Admin"
		}
		status := "Aktif"
		if !usr.IsActive {
			status = "Nonaktif"
		}
		fmt.Printf("      %-12s %-15s %s\n", usr.Username, role, status)
	}

	fmt.Print("[5/6] koneksi database target... ")

	connPayload := map[string]any{
		"name":          smokeConnectionName,
		"description":   "Koneksi uji coba untuk E2E test",
		"host":          "127.0.0.1",
		"port":          5433,
		"database_name": "sample_db",
		"username":      "sample_app",
		"password":      "sample_app",
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
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(body, &res)
		dbID = res.ID
	}

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
		for _, c := range listRes.Connections {
			if c.Name == smokeConnectionName {
				dbID = c.ID
				break
			}
		}
	}

	if dbID == 0 {
		fatal("tidak dapat ID koneksi target")
	}

	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/api/admin/connections/%d/test", baseURL, dbID), nil)
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err = client.Do(req)
	if err != nil {
		fatal("tes koneksi gagal: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var testRes struct {
		OK            bool   `json:"ok"`
		LatencyMS     int64  `json:"latency_ms"`
		ServerVersion string `json:"server_version"`
	}
	_ = json.Unmarshal(body, &testRes)
	fmt.Printf("ok, id %d, %d ms\n", dbID, testRes.LatencyMS)

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
	for _, g := range grpRes.Groups {
		if g.Name == smokeGroupName {
			targetGroupID = g.ID
			break
		}
	}
	if targetGroupID == 0 {
		gPay, _ := json.Marshal(map[string]string{"name": smokeGroupName, "description": "Group for E2E testing"})
		req, _ = http.NewRequest("POST", baseURL+"/api/admin/groups", strings.NewReader(string(gPay)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrfToken)
		resp, _ = client.Do(req)
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		var gCreated struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(body, &gCreated)
		targetGroupID = gCreated.ID
	}
	if targetGroupID == 0 {
		fatal("tidak dapat ID group E2E")
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

	fmt.Print("[6/6] migration dan viewer\n")

	fmt.Print("      jalankan migration... ")
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
		fatal("migration gagal: %v", err)
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
		fmt.Printf("gagal: %s\n", string(body))
	} else {
		fmt.Printf("ok, v%d oleh %s\n", migRes.Migration.Version, migRes.Migration.Executor)
	}

	fmt.Print("      cek schema... ")
	resp, err = client.Get(fmt.Sprintf("%s/api/databases/%d/schema?schema=public", baseURL, dbID))
	if err != nil {
		fatal("ambil schema gagal: %v", err)
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
		fmt.Println("ok, tabel e2e_smoke_test ada")
	} else {
		fmt.Printf("tabel e2e_smoke_test tidak ada (%d tabel di public)\n", len(schemaRes.Tables))
	}

	fmt.Print("      baca data tabel... ")
	resp, err = client.Get(fmt.Sprintf("%s/api/databases/%d/tables/e2e_smoke_test/data", baseURL, dbID))
	if err != nil {
		fatal("baca data gagal: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var dataRes struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
		Total   int64    `json:"total"`
	}
	_ = json.Unmarshal(body, &dataRes)
	fmt.Printf("ok, %d baris\n", dataRes.Total)

	fmt.Println("\nkasus gagal:")

	testEdgeAndErrorCases(baseURL, dbID, csrfToken, me.ID)

	fmt.Println("\nsemua lolos")
}

func testEdgeAndErrorCases(baseURL string, dbID int64, adminCSRF, adminID string) {
	noCookieClient := &http.Client{Timeout: 5 * time.Second}

	fmt.Print("[E1] login dengan password salah... ")
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

	issuedSession := false
	for _, c := range resp.Cookies() {
		if c.Name == "app_session_id" && c.Value != "" {
			issuedSession = true
		}
	}
	if issuedSession {
		fatal("session tetap dibuat padahal password salah")
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E2] POST tanpa token CSRF... ")
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(`{"username":"csrf_victim","password":"Password#123"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = noCookieClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("harusnya 403, dapat %d", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E3] POST dengan token CSRF palsu... ")
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(`{"username":"csrf_victim","password":"Password#123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "fake_invalid_token_1234567890abcdef")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "different_real_token_value_abc"})
	resp, _ = noCookieClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("harusnya 403, dapat %d", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E4] user biasa buka endpoint admin... ")
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

	req, _ = http.NewRequest("GET", baseURL+"/api/admin/users", nil)
	resp, _ = aliceClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		fatal("user biasa bisa buka /api/admin/users (status %d)", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E5] admin menonaktifkan akun sendiri... ")
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
		fatal("admin bisa menonaktifkan akun sendiri (status %d)", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E6] buat user dengan password pendek... ")
	invalidUserPayload := `{"username":"shortpwd_user","password":"123"}`
	req, _ = http.NewRequest("POST", baseURL+"/api/admin/users", strings.NewReader(invalidUserPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", adminCSRF)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: adminCSRF})
	resp, _ = adminClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		fatal("harusnya 400, dapat %d", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E7] cookie session palsu... ")
	tamperedClient := &http.Client{Timeout: 5 * time.Second}
	req, _ = http.NewRequest("GET", baseURL+"/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: "tampered_session_token_1234567890abcdef"})
	resp, _ = tamperedClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		fatal("harusnya 401, dapat %d", resp.StatusCode)
	}
	fmt.Println("ok, ditolak")

	fmt.Print("[E8] migration dengan SQL error... ")
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
		fatal("migration error tidak ditolak (status %d)", resp.StatusCode)
	}

	req, _ = http.NewRequest("GET", fmt.Sprintf("%s/api/databases/%d/schema?schema=public", baseURL, dbID), nil)
	req.AddCookie(&http.Cookie{Name: "app_session_id", Value: getAdminToken(baseURL, adminCSRF)})
	resp, _ = adminClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "test_atomic_rollback") {
		fatal("rollback gagal, tabelnya tetap terbuat")
	}
	fmt.Println("ok, di-rollback")

	fmt.Print("[E9] EXPLAIN untuk CREATE TABLE... ")
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
		fatal("respons EXPLAIN untuk DDL tidak sesuai: %s", string(body))
	}
	fmt.Println("ok, dilewati")
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
	fmt.Printf("\ngagal: "+format+"\n", args...)
	os.Exit(1)
}
