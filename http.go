package knockrd

import (
	crand "crypto/rand"
	"fmt"
	"html/template"
	"log"
	"net/http"
)

var (
	mux     = http.NewServeMux()
	backend Backend
	tmpl    = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8">
	<title>knockrd</title>
  </head>
  <body>
	<h1>knockrd</h1>
	<p>{{ .IPAddr }}</p>
	<form method="POST">
	  <input type="hidden" name="csrf_token" value="{{ .CSRFToken }}">
	  <input type="submit" value="Allow" name="allow">
	  <input type="submit" value="Disallow" name="disallow">
	</form>
  </body>
</html>
`))
)

func init() {
	mux.HandleFunc("/", wrap(rootHandler))
	mux.HandleFunc("/allow", wrap(allowHandler))
	mux.HandleFunc("/auth", wrap(authHandler))
}

type handler func(http.ResponseWriter, *http.Request) error

func wrap(h handler) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "private")
		err := h(w, r)
		if err != nil {
			log.Println("[error]", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, "Server Error")
		}
	}
}

type lambdaHandler struct {
	handler http.Handler
}

func (h lambdaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("[debug] remote_addr:%s", r.RemoteAddr)
	log.Printf("[debug] headers:%#v", r.Header)
	r.RemoteAddr = "127.0.0.1:0"
	h.handler.ServeHTTP(w, r)
}

func allowHandler(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		allowGetHandler(w, r)
	case http.MethodPost:
		allowPostHandler(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	return nil
}

func allowGetHandler(w http.ResponseWriter, r *http.Request) error {
	ipaddr := r.Header.Get("X-Real-IP")
	if ipaddr == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Bad request")
		return nil
	}
	token, err := csrfToken()
	if err != nil {
		return err
	}
	if err := backend.Set(token); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = tmpl.ExecuteTemplate(w, "form",
		struct {
			IPAddr    string
			CSRFToken string
		}{
			IPAddr:    ipaddr,
			CSRFToken: token,
		})
	if err != nil {
		return err
	}
	return nil
}

func allowPostHandler(w http.ResponseWriter, r *http.Request) error {
	ipaddr := r.Header.Get("X-Real-IP")
	token := r.FormValue("csrf_token")
	if ipaddr == "" || token == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Bad request")
		return nil
	}

	if ok, err := backend.Get(token); err != nil {
		return err
	} else if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Bad request")
		return nil
	}
	log.Println("[debug] CSRF token verified")
	if err := backend.Delete(token); err != nil {
		return err
	}

	if r.FormValue("allow") != "" {
		log.Println("[debug] setting allowed IP address", ipaddr)
		if err := backend.Set(ipaddr); err != nil {
			return err
		}
		log.Printf("[info] set allowed IP address for %s TTL %s", ipaddr, backend.TTL())
		fmt.Fprintf(w, "Allowed from %s for %s.\n", ipaddr, backend.TTL())
	} else if r.FormValue("disallow") != "" {
		log.Println("[debug] removing allowed IP address", ipaddr)
		if err := backend.Delete(ipaddr); err != nil {
			return err
		}
		log.Println("[info] remove allowed IP address", ipaddr)
		fmt.Fprintf(w, "Disallowed from %s\n", ipaddr)
	}
	return nil
}

func authHandler(w http.ResponseWriter, r *http.Request) error {
	ipaddr := r.Header.Get("X-Real-IP")
	if ok, err := backend.Get(ipaddr); err != nil {
		return err
	} else if !ok {
		log.Println("[info] not allowed IP address", ipaddr)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, "Forbidden")
		return nil
	}
	log.Println("[debug] allowed IP address", ipaddr)
	fmt.Fprintln(w, "OK")
	return nil
}

func rootHandler(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "knockrd alive from %s\n", r.Header.Get("X-Real-IP"))
	return nil
}

func csrfToken() (string, error) {
	k := make([]byte, 32)
	if _, err := crand.Read(k); err != nil {
		return "", err
	}
	return noCachePrefix + fmt.Sprintf("%x", k), nil
}
