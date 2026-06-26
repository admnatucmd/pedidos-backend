package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"net"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/sheets/v4"
	"google.golang.org/api/option"
)

// ============================================================
// ESTRUTURAS DE AUTENTICAÇÃO
// ============================================================

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Loja     string `json:"loja"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	User    string `json:"user,omitempty"`
	Loja    string `json:"loja,omitempty"`
}

type Pagamento struct {
	Pedido   string `json:"pedido"`
	Parcela  int    `json:"parcela"`
	Tipo     string `json:"tipo"`
	Pago     bool   `json:"pago"`
	DataHora string `json:"dataHora"`
}

// ============================================================
// CONFIGURAÇÕES
// ============================================================

var store *sessions.CookieStore

// ===== MAPA DE USUÁRIOS =====
var users = map[string]User{
	"pedidos872": {
		Username: "pedidos872",
		Password: hashPassword("capta872"),
		Role:     "admin",
		Loja:     "loja872sh",
	},
	"pedidos419": {
		Username: "pedidos419",
		Password: hashPassword("capta419"),
		Role:     "admin",
		Loja:     "loja419sm",
	},
	"pedidos168": {
		Username: "pedidos168",
		Password: hashPassword("capta168"),
		Role:     "admin",
		Loja:     "loja168mh",
	 },
}

// ===== MAPA DE PLANILHAS POR LOJA =====
var spreadsheetMap = map[string]string{
	"loja872sh": "1TAxNfLlG0hvUMHziMi2oMDeHt9AbKxamQOgZzefoYaI", // Planilha pedidos872
	"loja419sm": "1db1ES9_h5pyMh6P0OV59dfu2DWsciAIHsYZYzOjFZM4",
	"loja168mh": "1CFcwQwDJAbzApSaX0kbefSkOnNTxV5A8pfW-Qff4790",
}

// ===== MAPA DE CREDENCIAIS POR LOJA =====
var credenciaisSheets = map[string]string{
	"loja872sh": "CREDENTIALS_PEDIDOS872",
	"loja419sm": "CREDENTIALS_PEDIDOS419",
	"loja168mh": "CREDENTIALS_PEDIDOS168",
}

// Cache dos serviços do Sheets
var sheetsServices = make(map[string]*sheets.Service)

// ============================================================
// FUNÇÕES DE AUTENTICAÇÃO
// ============================================================

func hashPassword(password string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	return string(hash)
}

func checkPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func generateSecretKey() string {
	key := os.Getenv("SESSION_SECRET")
	if key == "" {
		key = "pedidos872-session-secret-32-bytes-key!!"
	}
	if len(key) < 32 {
		key = key + strings.Repeat("x", 32-len(key))
	} else if len(key) > 32 {
		key = key[:32]
	}
	return key
}

func initSessionStore() {
	secretKey := generateSecretKey()
	store = sessions.NewCookieStore([]byte(secretKey))
	
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   28800,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}
	
	log.Printf("🔐 Session Store configurado")
}

// ============================================================
// GOOGLE SHEETS - MULTI-LOJAS
// ============================================================

func getSheetsService(loja string) (*sheets.Service, error) {
	if service, exists := sheetsServices[loja]; exists {
		return service, nil
	}
	
	envVarName, exists := credenciaisSheets[loja]
	if !exists {
		return nil, fmt.Errorf("credencial não configurada para loja: %s", loja)
	}
	
	jsonCreds := os.Getenv(envVarName)
	if jsonCreds == "" {
		return nil, fmt.Errorf("credencial Google Sheets não encontrada para loja %s (variável: %s)", loja, envVarName)
	}
	
	service, err := initSheetsServiceWithCreds(jsonCreds)
	if err != nil {
		return nil, fmt.Errorf("erro ao inicializar serviço Sheets: %w", err)
	}
	
	sheetsServices[loja] = service
	log.Printf("✅ Serviço Sheets inicializado para loja: %s", loja)
	return service, nil
}

func initSheetsServiceWithCreds(jsonCreds string) (*sheets.Service, error) {
	ctx := context.Background()
	jsonCreds = strings.TrimSpace(jsonCreds)
	credsBytes := []byte(jsonCreds)
	
	config, err := google.JWTConfigFromJSON(credsBytes, sheets.SpreadsheetsScope)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar config JWT: %w", err)
	}
	
	client := config.Client(ctx)
	srv, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("erro ao criar serviço Sheets: %w", err)
	}
	
	return srv, nil
}

func getSpreadsheetID(loja string) string {
	if id, exists := spreadsheetMap[loja]; exists {
		return id
	}
	return ""
}

// ============================================================
// FUNÇÕES DE AUTENTICAÇÃO - HANDLERS
// ============================================================

func getClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ips := strings.Split(forwarded, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ============================================================
// CORS MIDDLEWARE
// ============================================================

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		
		allowedOrigins := []string{
			"https://pedidos872.gtgo.com.br",
			"https://gtgo.com.br",
			"https://www.gtgo.com.br",
			"http://localhost:3000",
			"http://localhost:8080",
		}
		
		isAllowed := false
		for _, allowed := range allowedOrigins {
			if origin == allowed {
				isAllowed = true
				break
			}
		}
		
		if !isAllowed && strings.HasSuffix(origin, ".gtgo.com.br") {
			isAllowed = true
		}
		
		if isAllowed && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "https://pedidos872.gtgo.com.br")
		}
		
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		
		next(w, r)
	}
}

// ============================================================
// HANDLERS DE AUTENTICAÇÃO
// ============================================================

func loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	if r.Method != "POST" {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	
	var loginReq LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&loginReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro ao processar requisição",
		})
		return
	}
	
	if loginReq.Username == "" || loginReq.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Usuário e senha são obrigatórios",
		})
		return
	}
	
	user, exists := users[loginReq.Username]
	if !exists {
		log.Printf("❌ Tentativa de login com usuário inexistente: %s (IP: %s)", loginReq.Username, getClientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Usuário ou senha incorretos",
		})
		return
	}
	
	if !checkPasswordHash(loginReq.Password, user.Password) {
		log.Printf("❌ Tentativa de login com senha incorreta: %s (IP: %s)", loginReq.Username, getClientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Usuário ou senha incorretos",
		})
		return
	}
	
	session, err := store.Get(r, "session-pedidos872")
	if err != nil {
		log.Printf("❌ Erro ao obter sessão: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro interno do servidor",
		})
		return
	}
	
	session.Values["authenticated"] = true
	session.Values["username"] = user.Username
	session.Values["role"] = user.Role
	session.Values["loja"] = user.Loja
	
	session.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   28800,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}
	
	if err := session.Save(r, w); err != nil {
		log.Printf("❌ Erro ao salvar sessão: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro ao criar sessão",
		})
		return
	}
	
	log.Printf("✅ Login bem-sucedido: %s (Loja: %s, IP: %s)", user.Username, user.Loja, getClientIP(r))
	
	json.NewEncoder(w).Encode(AuthResponse{
		Success: true,
		Message: "Login realizado com sucesso",
		User:    user.Username,
		Loja:    user.Loja,
	})
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	session, err := store.Get(r, "session-pedidos872")
	if err != nil {
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro ao obter sessão",
		})
		return
	}
	
	session.Values["authenticated"] = false
	session.Values["username"] = ""
	session.Values["role"] = ""
	session.Values["loja"] = ""
	session.Options.MaxAge = -1
	
	if err := session.Save(r, w); err != nil {
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro ao fazer logout",
		})
		return
	}
	
	json.NewEncoder(w).Encode(AuthResponse{
		Success: true,
		Message: "Logout realizado com sucesso",
	})
}

func checkAuthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
	session, err := store.Get(r, "session-pedidos872")
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Erro de sessão",
		})
		return
	}
	
	auth, ok := session.Values["authenticated"].(bool)
	if !ok || !auth {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthResponse{
			Success: false,
			Message: "Não autenticado",
		})
		return
	}
	
	username, _ := session.Values["username"].(string)
	loja, _ := session.Values["loja"].(string)
	
	json.NewEncoder(w).Encode(AuthResponse{
		Success: true,
		Message: "Autenticado",
		User:    username,
		Loja:    loja,
	})
}

// ============================================================
// FUNÇÃO PARA EXTRAIR LOJA DO CONTEXTO
// ============================================================

func getLojaFromRequest(r *http.Request) string {
	session, err := store.Get(r, "session-pedidos872")
	if err != nil {
		return ""
	}
	
	if loja, ok := session.Values["loja"].(string); ok {
		return loja
	}
	return ""
}

// ============================================================
// FUNÇÃO DE AUTENTICAÇÃO - MIDDLEWARE
// ============================================================

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r, "session-pedidos872")
		if err != nil {
			http.Error(w, "Erro de sessão", http.StatusInternalServerError)
			return
		}
		
		auth, ok := session.Values["authenticated"].(bool)
		if !ok || !auth {
			http.Error(w, "Não autorizado", http.StatusUnauthorized)
			return
		}
		
		next(w, r)
	}
}

// ============================================================
// HANDLERS DE PAGAMENTOS (COM MULTI-LOJA)
// ============================================================

func handlePagamentos(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	
	loja := getLojaFromRequest(r)
	if loja == "" {
		http.Error(w, "Loja não identificada", http.StatusUnauthorized)
		return
	}
	
	pagamentos, err := buscarPagamentos(loja)
	if err != nil {
		log.Printf("Erro ao buscar pagamentos da loja %s: %v", loja, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Pagamento{})
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagamentos)
}

func handleSalvarPagamento(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}
	
	loja := getLojaFromRequest(r)
	if loja == "" {
		http.Error(w, "Loja não identificada", http.StatusUnauthorized)
		return
	}
	
	var pag Pagamento
	if err := json.NewDecoder(r.Body).Decode(&pag); err != nil {
		http.Error(w, "Erro ao ler dados: "+err.Error(), http.StatusBadRequest)
		return
	}
	
	if pag.Pedido == "" || pag.Parcela <= 0 || pag.Tipo == "" {
		http.Error(w, "Dados inválidos", http.StatusBadRequest)
		return
	}
	
	pag.DataHora = time.Now().Format("02/01/2006 15:04:05")
	
	if err := salvarPagamento(loja, pag); err != nil {
		log.Printf("Erro ao salvar pagamento na loja %s: %v", loja, err)
		http.Error(w, "Erro ao salvar: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Pagamento salvo com sucesso!",
	})
}

// ============================================================
// FUNÇÕES DE ACESSO AO GOOGLE SHEETS
// ============================================================

func buscarPagamentos(loja string) ([]Pagamento, error) {
	srv, err := getSheetsService(loja)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar cliente Google Sheets: %w", err)
	}
	
	spreadsheetID := getSpreadsheetID(loja)
	if spreadsheetID == "" {
		return nil, fmt.Errorf("planilha não configurada para loja: %s", loja)
	}
	
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "Pagamentos").Do()
	if err != nil {
		log.Printf("Aba 'Pagamentos' não encontrada, retornando vazio")
		return []Pagamento{}, nil
	}
	
	var pagamentos []Pagamento
	for i, row := range resp.Values {
		if i == 0 {
			continue
		}
		if len(row) < 4 {
			continue
		}
		
		pago := false
		if len(row) > 3 {
			val := fmt.Sprintf("%v", row[3])
			pago = val == "TRUE" || val == "true" || val == "1"
		}
		
		dataHora := ""
		if len(row) > 4 {
			dataHora = fmt.Sprintf("%v", row[4])
		}
		
		parcela := 0
		switch v := row[1].(type) {
		case float64:
			parcela = int(v)
		case string:
			fmt.Sscanf(v, "%d", &parcela)
		}
		
		pagamentos = append(pagamentos, Pagamento{
			Pedido:   fmt.Sprintf("%v", row[0]),
			Parcela:  parcela,
			Tipo:     fmt.Sprintf("%v", row[2]),
			Pago:     pago,
			DataHora: dataHora,
		})
	}
	
	log.Printf("✅ %d pagamentos carregados da loja %s", len(pagamentos), loja)
	return pagamentos, nil
}

func salvarPagamento(loja string, pag Pagamento) error {
	srv, err := getSheetsService(loja)
	if err != nil {
		return fmt.Errorf("erro ao criar cliente Google Sheets: %w", err)
	}
	
	spreadsheetID := getSpreadsheetID(loja)
	if spreadsheetID == "" {
		return fmt.Errorf("planilha não configurada para loja: %s", loja)
	}
	
	_, err = srv.Spreadsheets.Values.Get(spreadsheetID, "Pagamentos").Do()
	if err != nil {
		_, err = srv.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
			Requests: []*sheets.Request{
				{
					AddSheet: &sheets.AddSheetRequest{
						Properties: &sheets.SheetProperties{
							Title: "Pagamentos",
						},
					},
				},
			},
		}).Do()
		if err != nil {
			return fmt.Errorf("erro ao criar aba: %w", err)
		}
		
		_, err = srv.Spreadsheets.Values.Update(spreadsheetID, "Pagamentos!A1:E1", &sheets.ValueRange{
			Values: [][]interface{}{
				{"Pedido", "Parcela", "Tipo", "Pago", "DataHora"},
			},
		}).ValueInputOption("RAW").Do()
		if err != nil {
			return fmt.Errorf("erro ao adicionar cabeçalho: %w", err)
		}
	}
	
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, "Pagamentos").Do()
	if err != nil {
		return fmt.Errorf("erro ao buscar dados existentes: %w", err)
	}
	
	for i, row := range resp.Values {
		if i == 0 {
			continue
		}
		if len(row) >= 3 {
			pedido := fmt.Sprintf("%v", row[0])
			parcela := 0
			switch v := row[1].(type) {
			case float64:
				parcela = int(v)
			case string:
				fmt.Sscanf(v, "%d", &parcela)
			}
			tipo := fmt.Sprintf("%v", row[2])
			
			if pedido == pag.Pedido && parcela == pag.Parcela && tipo == pag.Tipo {
				_, err = srv.Spreadsheets.Values.Update(spreadsheetID, fmt.Sprintf("Pagamentos!D%d:E%d", i+1, i+1), &sheets.ValueRange{
					Values: [][]interface{}{
						{pag.Pago, pag.DataHora},
					},
				}).ValueInputOption("RAW").Do()
				if err != nil {
					return fmt.Errorf("erro ao atualizar registro: %w", err)
				}
				log.Printf("✅ Pagamento atualizado na loja %s: %s - Parcela %d - %s", loja, pag.Pedido, pag.Parcela, pag.Tipo)
				return nil
			}
		}
	}
	
	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, "Pagamentos!A:E", &sheets.ValueRange{
		Values: [][]interface{}{
			{pag.Pedido, pag.Parcela, pag.Tipo, pag.Pago, pag.DataHora},
		},
	}).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("erro ao inserir novo registro: %w", err)
	}
	
	log.Printf("✅ Novo pagamento inserido na loja %s: %s - Parcela %d - %s", loja, pag.Pedido, pag.Parcela, pag.Tipo)
	return nil
}

// ============================================================
// HANDLER DE STATUS
// ============================================================

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format("02/01/2006 15:04:05"),
		"lojas":  len(users),
	})
}

// ============================================================
// MAIN
// ============================================================

func main() {
	initSessionStore()
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	log.Printf("🚀 Servidor Multi-Lojas rodando na porta %s", port)
	log.Printf("🏪 Lojas configuradas: %d", len(users))
	for username := range users {
		log.Printf("   ✅ %s", username)
	}
	
	http.HandleFunc("/api/login", corsMiddleware(loginHandler))
	http.HandleFunc("/api/logout", corsMiddleware(logoutHandler))
	http.HandleFunc("/api/check-auth", corsMiddleware(checkAuthHandler))
	http.HandleFunc("/api/status", corsMiddleware(handleStatus))
	
	http.HandleFunc("/api/pagamentos", corsMiddleware(authMiddleware(handlePagamentos)))
	http.HandleFunc("/api/pagamento", corsMiddleware(authMiddleware(handleSalvarPagamento)))
	
	log.Printf("✅ Servidor pronto!")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}