package server

import (
	"github.com/gorilla/mux"
	"io/ioutil"
	"net/http"
	"stripe-ctf.com/sqlcluster/log"
	"stripe-ctf.com/sqlcluster/transport"
)

type Minimal struct {
	path             string
	listen           string
	router           *mux.Router
	httpServer       *http.Server

}

func NewMinimal(path, listen string) (*Minimal, error) {
	s := &Minimal{
		path:             path,
		listen:           listen,
		router:           mux.NewRouter(),
	}
	return s, nil
}

func (s *Minimal) ListenAndServe(join string) error {
	log.Printf("Start HTTP server")
	s.httpServer = &http.Server{
		Handler: s.router,
	}

	s.router.HandleFunc("/sql", s.sqlHandler).Methods("POST")
	
	l, err := transport.Listen(s.listen)
	if err != nil {
		log.Fatal(err)
	}

	return s.httpServer.Serve(l)
}


func (s *Minimal) sqlHandler(w http.ResponseWriter, req *http.Request) {
	log.Printf("HARNESS CALLED IN")
	ioutil.ReadAll(req.Body)
	http.Error(w, "Just a follower", http.StatusBadRequest)
}
