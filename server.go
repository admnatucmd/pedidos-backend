package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"os"   
	"google.golang.org/api/sheets/v4"
	"google.golang.org/api/option"
)

// Estrutura para o pagamento
type Pagamento struct {
	Pedido   string `json:"pedido"`
	Parcela  int    `json:"parcela"`
	Tipo     string `json:"tipo"`     // produto, royalties, propaganda
	Pago     bool   `json:"pago"`
	DataHora string `json:"dataHora"`
}

// Configuração da planilha
const (
	spreadsheetID = "1TAxNfLlG0hvUMHziMi2oMDeHt9AbKxamQOgZzefoYaI"
	sheetName     = "Pagamentos"
)

func main() {
	// Servir arquivos estáticos
	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	// Endpoint para buscar pagamentos
	http.HandleFunc("/api/pagamentos", corsMiddleware(handlePagamentos))
	
	// Endpoint para salvar pagamento
	http.HandleFunc("/api/pagamento", corsMiddleware(handleSalvarPagamento))

	// Endpoint para verificar status
	http.HandleFunc("/api/status", corsMiddleware(handleStatus))

	// ===== NOVO: Usar porta do Render =====
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"  // fallback para local
	}

	fmt.Printf("🚀 Servidor rodando em http://localhost:%s\n", port)
	fmt.Println("📊 Conectado ao Google Sheets")
	fmt.Println("📁 Servindo arquivos da pasta atual")
	fmt.Println("Pressione Ctrl+C para parar")
	
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Middleware CORS
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// Handler para verificar status
func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format("02/01/2006 15:04:05"),
		"sheet":  spreadsheetID,
	})
}

// Handler para buscar pagamentos
func handlePagamentos(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	pagamentos, err := buscarPagamentos()
	if err != nil {
		log.Printf("Erro ao buscar pagamentos: %v", err)
		// Retorna array vazio em caso de erro
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]Pagamento{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pagamentos)
}

// Handler para salvar pagamento
func handleSalvarPagamento(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	var pag Pagamento
	if err := json.NewDecoder(r.Body).Decode(&pag); err != nil {
		http.Error(w, "Erro ao ler dados: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validar dados
	if pag.Pedido == "" {
		http.Error(w, "Pedido é obrigatório", http.StatusBadRequest)
		return
	}
	if pag.Parcela <= 0 {
		http.Error(w, "Parcela inválida", http.StatusBadRequest)
		return
	}
	if pag.Tipo == "" {
		http.Error(w, "Tipo é obrigatório", http.StatusBadRequest)
		return
	}

	pag.DataHora = time.Now().Format("02/01/2006 15:04:05")

	if err := salvarPagamento(pag); err != nil {
		log.Printf("Erro ao salvar pagamento: %v", err)
		
		// Verificar se é erro de autenticação
		if strings.Contains(err.Error(), "authentication") || strings.Contains(err.Error(), "credentials") {
			http.Error(w, "Erro de autenticação: verifique o arquivo credentials.json", http.StatusInternalServerError)
			return
		}
		
		http.Error(w, "Erro ao salvar: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "Pagamento salvo com sucesso!",
	})
}

// Buscar pagamentos da planilha
func buscarPagamentos() ([]Pagamento, error) {
	ctx := context.Background()
	srv, err := getSheetsService(ctx)
	if err != nil {
		return nil, fmt.Errorf("erro ao criar cliente Google Sheets: %w", err)
	}

	// Verificar se a aba existe
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, sheetName).Do()
	if err != nil {
		// Aba não existe, retorna vazio
		log.Printf("Aba '%s' não encontrada, retornando vazio", sheetName)
		return []Pagamento{}, nil
	}

	var pagamentos []Pagamento
	for i, row := range resp.Values {
		if i == 0 { // pular cabeçalho
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
		
		// Converter parcela para int
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
	
	log.Printf("✅ %d pagamentos carregados da planilha", len(pagamentos))
	return pagamentos, nil
}

// Salvar pagamento na planilha
func salvarPagamento(pag Pagamento) error {
	ctx := context.Background()
	srv, err := getSheetsService(ctx)
	if err != nil {
		return fmt.Errorf("erro ao criar cliente Google Sheets: %w", err)
	}

	// Verificar se a aba existe, se não, criar
	_, err = srv.Spreadsheets.Values.Get(spreadsheetID, sheetName).Do()
	if err != nil {
		log.Printf("Criando aba '%s'...", sheetName)
		
		// Criar aba
		_, err = srv.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
			Requests: []*sheets.Request{
				{
					AddSheet: &sheets.AddSheetRequest{
						Properties: &sheets.SheetProperties{
							Title: sheetName,
						},
					},
				},
			},
		}).Do()
		if err != nil {
			return fmt.Errorf("erro ao criar aba: %w", err)
		}
		
		// Adicionar cabeçalho
		_, err = srv.Spreadsheets.Values.Update(spreadsheetID, sheetName+"!A1:E1", &sheets.ValueRange{
			Values: [][]interface{}{
				{"Pedido", "Parcela", "Tipo", "Pago", "DataHora"},
			},
		}).ValueInputOption("RAW").Do()
		if err != nil {
			return fmt.Errorf("erro ao adicionar cabeçalho: %w", err)
		}
		
		log.Printf("✅ Aba '%s' criada com cabeçalho", sheetName)
	}

	// Buscar dados existentes para atualizar ou inserir
	resp, err := srv.Spreadsheets.Values.Get(spreadsheetID, sheetName).Do()
	if err != nil {
		return fmt.Errorf("erro ao buscar dados existentes: %w", err)
	}

	// Procurar se já existe registro para este pedido+parcela+tipo
	for i, row := range resp.Values {
		if i == 0 { // pular cabeçalho
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
				// Atualizar existente
				_, err = srv.Spreadsheets.Values.Update(spreadsheetID, fmt.Sprintf("%s!D%d:E%d", sheetName, i+1, i+1), &sheets.ValueRange{
					Values: [][]interface{}{
						{pag.Pago, pag.DataHora},
					},
				}).ValueInputOption("RAW").Do()
				if err != nil {
					return fmt.Errorf("erro ao atualizar registro: %w", err)
				}
				log.Printf("✅ Pagamento atualizado: %s - Parcela %d - %s", pag.Pedido, pag.Parcela, pag.Tipo)
				return nil
			}
		}
	}

	// Inserir novo registro
	_, err = srv.Spreadsheets.Values.Append(spreadsheetID, sheetName+"!A:E", &sheets.ValueRange{
		Values: [][]interface{}{
			{pag.Pedido, pag.Parcela, pag.Tipo, pag.Pago, pag.DataHora},
		},
	}).ValueInputOption("RAW").Do()
	if err != nil {
		return fmt.Errorf("erro ao inserir novo registro: %w", err)
	}
	
	log.Printf("✅ Novo pagamento inserido: %s - Parcela %d - %s", pag.Pedido, pag.Parcela, pag.Tipo)
	return nil
}

// Criar cliente do Google Sheets
func getSheetsService(ctx context.Context) (*sheets.Service, error) {
	// Tentar usar credenciais do arquivo
	credsFile := "credentials.json"
	
	// Verificar se o arquivo existe
	return sheets.NewService(ctx, option.WithCredentialsFile(credsFile))


}

