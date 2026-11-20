package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/events"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

const maxStoredPayload = 64 * 1024

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "corpo grande demais"})
		return
	}

	record := store.Event{
		EventID:    r.Header.Get(resgata.EventIDHeader),
		DeliveryID: r.Header.Get(resgata.DeliveryHeader),
		Payload:    truncate(string(body)),
	}
	record.Attempt, _ = strconv.Atoi(r.Header.Get(resgata.AttemptHeader))

	if err := resgata.Verify(s.cfg.WebhookSecret, r.Header.Get(resgata.SignatureHeader), body, s.now()); err != nil {
		record.EventID = ""
		record.Reason = err.Error()
		if s.local != nil {
			if _, _, storeErr := s.local.ReceiveEvent(record); storeErr != nil {
				log.Printf("guardando webhook rejeitado: %v", storeErr)
			}
		} else {
			log.Printf("webhook rejeitado: %v", err)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	var event resgata.Event
	if err := json.Unmarshal(body, &event); err != nil {
		record.Reason = "corpo não é um evento JSON válido"
		record.SignatureOK = true
		if s.local != nil {
			_, _, _ = s.local.ReceiveEvent(record)
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": record.Reason})
		return
	}
	if record.EventID == "" {
		record.EventID = event.ID
	}
	record.Type = event.Type
	record.SignatureOK = true

	st, routed := s.storeForEvent(event)
	if !routed {
		log.Printf("webhook %s %s ignorado: nenhum workspace para o external_ref %q", event.Type, event.ID, event.Connection.ExternalRef)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}

	if s.cfg.PortalMode() && event.Type == resgata.EventConnectionRevoked {
		if _, hasConnection, _ := st.Connection(); !hasConnection {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
			return
		}
	}

	stored, isNew, err := st.ReceiveEvent(record)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !isNew && stored.Processed {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	outcome, err := events.Apply(st, event)
	if err != nil {
		log.Printf("aplicando %s %s: %v", event.Type, event.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "falha ao processar, tente de novo"})
		return
	}
	if err := st.MarkEventProcessed(stored.ID, outcome); err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func truncate(payload string) string {
	if len(payload) > maxStoredPayload {
		return payload[:maxStoredPayload]
	}
	return payload
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
