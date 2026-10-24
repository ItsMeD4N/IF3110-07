package csrf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRF_SafeMethods(t *testing.T) {
	middleware := Middleware(false)
	var capturedToken string

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = Token(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("safe method ok"))
	}))

	// 1. GET request tanpa cookie CSRF awal -> harus meloloskan dan memasang cookie
	req := httptest.NewRequest("GET", "/test-safe", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET harus lolos dengan 200 OK, got %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == CSRFCookieName {
			csrfCookie = c
			break
		}
	}

	if csrfCookie == nil {
		t.Fatalf("Cookie %s harus otomatis dibuat pada GET request", CSRFCookieName)
	}
	if csrfCookie.HttpOnly {
		t.Errorf("Cookie CSRF harus HttpOnly = false agar bisa dibaca client-side JS")
	}
	if capturedToken == "" || capturedToken != csrfCookie.Value {
		t.Fatalf("Token dalam context (%s) tidak cocok dengan cookie (%s)", capturedToken, csrfCookie.Value)
	}

	// 2. OPTIONS request (CORS Preflight)
	reqOptions := httptest.NewRequest("OPTIONS", "/test-safe", nil)
	recOptions := httptest.NewRecorder()
	handler.ServeHTTP(recOptions, reqOptions)
	if recOptions.Code != http.StatusOK {
		t.Fatalf("OPTIONS harus lolos dengan 200 OK, got %d", recOptions.Code)
	}
}

func TestCSRF_MutationMethods_Rejections(t *testing.T) {
	middleware := Middleware(false)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mutation success"))
	}))

	methods := []string{"POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		// Skenario A: Tanpa cookie sama sekali -> 403
		reqNoCookie := httptest.NewRequest(method, "/api/mutation", nil)
		recNoCookie := httptest.NewRecorder()
		handler.ServeHTTP(recNoCookie, reqNoCookie)

		if recNoCookie.Code != http.StatusForbidden {
			t.Errorf("[%s] Harusnya 403 Forbidden tanpa cookie, got %d", method, recNoCookie.Code)
		}

		// Skenario B: Cookie ada, tapi header/form token tidak dikirim -> 403
		reqMissingToken := httptest.NewRequest(method, "/api/mutation", nil)
		reqMissingToken.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "valid-cookie-token-1234567890"})
		recMissingToken := httptest.NewRecorder()
		handler.ServeHTTP(recMissingToken, reqMissingToken)

		if recMissingToken.Code != http.StatusForbidden {
			t.Errorf("[%s] Harusnya 403 Forbidden tanpa client token, got %d", method, recMissingToken.Code)
		}

		// Skenario C: Token header salah / tidak cocok dengan cookie -> 403
		reqWrongToken := httptest.NewRequest(method, "/api/mutation", nil)
		reqWrongToken.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "valid-cookie-token-1234567890"})
		reqWrongToken.Header.Set(CSRFHeaderName, "attacker-mismatched-token-999")
		recWrongToken := httptest.NewRecorder()
		handler.ServeHTTP(recWrongToken, reqWrongToken)

		if recWrongToken.Code != http.StatusForbidden {
			t.Errorf("[%s] Harusnya 403 Forbidden jika token tidak cocok, got %d", method, recWrongToken.Code)
		}
	}
}

func TestCSRF_MutationMethods_Acceptance(t *testing.T) {
	middleware := Middleware(false)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mutation success"))
	}))

	token, _ := GenerateToken()

	// Skenario 1: POST dengan header X-CSRF-Token
	reqHeader := httptest.NewRequest("POST", "/api/mutation", nil)
	reqHeader.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	reqHeader.Header.Set(CSRFHeaderName, token)
	recHeader := httptest.NewRecorder()
	handler.ServeHTTP(recHeader, reqHeader)

	if recHeader.Code != http.StatusOK {
		t.Fatalf("POST dengan header X-CSRF-Token valid harus 200 OK, got %d. Body: %s", recHeader.Code, recHeader.Body.String())
	}

	// Skenario 2: POST dengan Form Field csrf_token
	formData := url.Values{}
	formData.Set(CSRFFormField, token)
	formData.Set("data", "hello")

	reqForm := httptest.NewRequest("POST", "/form-submit", strings.NewReader(formData.Encode()))
	reqForm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqForm.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	recForm := httptest.NewRecorder()
	handler.ServeHTTP(recForm, reqForm)

	if recForm.Code != http.StatusOK {
		t.Fatalf("POST dengan Form Field csrf_token valid harus 200 OK, got %d. Body: %s", recForm.Code, recForm.Body.String())
	}
}

func TestCSRF_TemplateField(t *testing.T) {
	dummyToken := "test-token-abcdef123456"
	htmlField := TemplateField(dummyToken)

	expectedSubstring := `type="hidden" name="csrf_token" value="test-token-abcdef123456"`
	if !strings.Contains(string(htmlField), expectedSubstring) {
		t.Fatalf("TemplateField tidak menghasilkan elemen HTML hidden input yang benar: %s", htmlField)
	}
}

func TestCSRF_Endpoint(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	// Wrap dengan CSRF middleware
	handler := Middleware(false)(mux)

	req := httptest.NewRequest("GET", "/api/csrf-token", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/csrf-token harus 200 OK, got %d", rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Response harus berupa valid JSON: %v", err)
	}

	if resp["csrf_token"] == "" {
		t.Fatalf("Response JSON harus menyertakan csrf_token")
	}
}
