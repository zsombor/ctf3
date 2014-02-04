package server

import (
	"fmt"
	"github.com/goraft/raft"
	"github.com/gorilla/mux"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strconv"
	"stripe-ctf.com/sqlcluster/log"
	"stripe-ctf.com/sqlcluster/sql"
	"stripe-ctf.com/sqlcluster/transport"
	"stripe-ctf.com/sqlcluster/util"
	"sync"
	"time"
	"bytes"
	"io"
)


type Server struct {
	path             string
	listen           string
	connectionString string
	router           *mux.Router
	httpServer       *http.Server
	sql              *sql.SQL
	client           *transport.Client
	raftServer       raft.Server
	forwardCnt       int
	forwMutex        sync.Mutex
	forwBucketFull   bool
	forwFailed       *FailedProxiedCommands
	pendingCnt       int
	pendingMutex     sync.Mutex
}



// Creates a new server.
func New(path, listen string) (*Server, error) {
	sqlPath := filepath.Join(path, "storage.sql")
	util.EnsureAbsent(sqlPath)

	s := &Server{
		path:             path,
		listen:           listen,
		sql:              sql.NewSQL(sqlPath),
		router:           mux.NewRouter(),
		client:           transport.NewClient(),
		connectionString: transport.ConnectionString(listen),
		forwFailed:       NewFailedProxiedCommands(),
	}

	return s, nil
}

// Starts the server.
func (s *Server) ListenAndServe(primary string) error {
	var err error
	raftTransporter := NewRaftHTTPTransporter("/raft")
	s.raftServer, err = raft.NewServer(
		s.path,
		s.path,
		raftTransporter,
		nil, s, s.connectionString)
	if err != nil {
		log.Fatal(err)
	}
	s.raftServer.SetHeartbeatInterval(500 * time.Millisecond)
	s.raftServer.SetElectionTimeout(2000 * time.Millisecond)
	raftTransporter.Install(s.raftServer, s)
	s.raftServer.Start()
	
	go func (){
		if primary == "" {
			s.initCluster()
			return
		}
		if err := s.Join(primary); err != nil {
			log.Fatal(err)
		}
	}()

	// go func() {
	// 	for _ = range time.Tick(1*time.Second) {
	// 		log.Printf("I think my leader is: %s", s.raftServer.Leader())
	// 		s.pendingMutex.Lock()
	// 		if s.pendingCnt > 0 {
	// 			log.Printf("I'm blocking on %d testharness connections", s.pendingCnt) 
	// 			log.Printf("QuorumSize=%d", s.raftServer.QuorumSize()) 
				
	// 		}
	// 		s.pendingMutex.Unlock()
	// 		s.forwFailed.Log()
	// 	}
	// }()
	
	log.Printf("Start HTTP server")
	s.httpServer = &http.Server{
		Handler: s.router,
	}

	//s.router.HandleFunc("/sql", s.sqlHandler).Methods("POST")
	s.router.HandleFunc("/sql", s.forwardHandler).Methods("POST")
	s.router.HandleFunc("/forw", s.forwardHandler).Methods("POST")
	s.router.HandleFunc("/join", s.joinHandler).Methods("POST")
	s.router.HandleFunc("/confirm", s.confirmHandler).Methods("POST")

	// Start Unix transport
	l, err := transport.Listen(s.listen)
	if err != nil {
		log.Fatal(err)
	}

	return s.httpServer.Serve(l)
}

// Satisfy raft's HTTPMuxer interface.
func (s *Server) HandleFunc(path string, f func(http.ResponseWriter, *http.Request)) {
	s.router.HandleFunc(path, f)
}

// Join an existing cluster
func (s *Server) Join(primary string) error {
	join := &raft.DefaultJoinCommand{
		Name:             s.raftServer.Name(),
		ConnectionString: s.connectionString,
	}

	cs, err := transport.Encode(primary)
	if err != nil {
		return err
	}
	for {
		if "" != s.raftServer.Leader(){
			return nil
		}
		log.Printf("Attempting to join cluster: %s", cs)
		err := s.postWithTimeout(cs, "/join", join, 10000 * time.Millisecond, 5000 * time.Millisecond)
		if  err == nil {
			log.Printf("Sent join request")
			continue
		}
		log.Printf("Unable to join cluster: %s", err)
		time.Sleep(1000 * time.Millisecond)
	}
}


func (s *Server) joinHandler(w http.ResponseWriter, req *http.Request) {
	join := &raft.DefaultJoinCommand{}
	if err := util.JSONDecode(req.Body, join); err != nil {
		log.Printf("Invalid join request: %s", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Got join request from %s", join.Name)
	if _, err := s.raftServer.Do(join); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Accepted join request from %s", join.Name)
}

type Receipt struct {
	Sql string
	Res []byte
}
func (s *Server) confirmHandler(w http.ResponseWriter, req *http.Request) {
	receipt := &Receipt{}
	if err := util.JSONDecode(req.Body, receipt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("Got Receipt")
	s.forwFailed.Rescue(receipt.Sql, receipt.Res)
	w.Write([]byte("OK"))
}

func (s *Server) Confirm(cs string, receipt *Receipt) {
	s.client.SafePost(cs, "/confirm", util.JSONEncode(receipt))
}



// // This is the only user-facing function, and accordingly the body is
// // a raw string rather than JSON.
// func (s *Server) sqlHandler(w http.ResponseWriter, req *http.Request) {
// 	query, err := ioutil.ReadAll(req.Body)
// 	if err != nil {
// 		log.Printf("Couldn't read body: %s", err)
// 		http.Error(w, err.Error(), http.StatusBadRequest)
// 		return
// 	}
// 	s.sendToRaft(query, w)
// }

type RaftRes struct {
	Res interface{}
	Error error
}
func (s *Server) sendToRaft(sql []byte, originalRequestor string, w http.ResponseWriter) {
	done := make(chan *RaftRes)
	if originalRequestor == "" {
		originalRequestor = s.connectionString
	}
	go func() {
		resp, err := s.raftServer.Do(NewCommand(string(sql), originalRequestor))
		done <- &RaftRes{Res: resp, Error: err}
	} ()
	timeout := time.After(6 * time.Second)
	select {
	case rr := <- done:
		if rr.Error != nil {
			http.Error(w, rr.Error.Error(), http.StatusBadRequest)
			return
		}
		response := rr.Res.([]byte)
		s.forwFailed.Clear(string(sql))
		w.Write(response)
		return
	case response:= <- s.forwFailed.ConfirmChan(string(sql)):
		log.Printf("Rescued request response from the new leader. Origin=%s", originalRequestor)
		w.Write(response)
		s.forwFailed.Clear(string(sql))
		return
	case <- timeout:
		http.Error(w, "sendToRaft timedout", http.StatusBadRequest)
		s.forwFailed.Clear(string(sql))
		return
	}
}


const nsInASecond = 1000000000

// is this cheating???
func (s *Server) forwardHandler(w http.ResponseWriter, req *http.Request) {
	s.pendingMutex.Lock()
	s.pendingCnt += 1
	s.pendingMutex.Unlock()
	defer func() {
		s.pendingMutex.Lock()
		s.pendingCnt -= 1
		s.pendingMutex.Unlock()
	}()

	query, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Printf("Couldn't read body: %s", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	//log.Printf("Test harness called in: sql=%s", string(query))

	requestedAt := s.originalRequestAt(req)
	if time.Now().Sub(requestedAt) > 3000 * time.Millisecond {
		message := "Ignoring old request"
		log.Printf(message)
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	originalRequestor := s.originalRequestor(req)
	state := s.raftServer.State()
	if state == "leader" {
		s.sendToRaft(query, originalRequestor, w)
		return
	}

	leaderName := s.raftServer.Leader()
	leader := s.raftServer.Peers()[leaderName]
	canForward := false
	if leader != nil && leaderName != "" {
		for i := 0; i < 2; i++ {
			if s.canForward() {
				defer s.decForwardCnt()
				canForward = true
				break
			}
			//log.Printf("Delaying response")
			time.Sleep(50 * time.Millisecond)
			//log.Printf("Woke up after delaying response")
		}
	}

	state = s.raftServer.State()
	if state == "leader" {
		s.sendToRaft(query, originalRequestor, w)
		return
	}

	leaderName = s.raftServer.Leader()
	leader = s.raftServer.Peers()[leaderName]
	message := ""
	if leader == nil || leader.ConnectionString == "" {
		message = "I got no leader currently"
	} else if canForward == false && state == "follower" {
		message = "I got too many requests pending forward"
	} else if s.forwBucketFull == true {
		message = "I'm not allowed to forward due to a recent timeout"
	} 
	if message != "" {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	

	proxyAt := time.Now()
	log.Printf("I got a leader <%s> attempting to proxy the request. sql=%s", leaderName, string(query))
	proxyResp, err := s.postTimeoutAndWithForwardHeader(leader.ConnectionString, 
		"/forw", requestedAt, originalRequestor, query) 
	if err != nil {
		log.Printf("Failed to proxy request after %0.1fms! %s", time.Now().Sub(proxyAt).Seconds()*1000, string(query))
		data := s.forwFailed.WaitFor(string(query))
		ms := time.Now().Sub(proxyAt).Seconds()*1000
		if data != nil {
			log.Printf("Recovered after the raft log had reached us in %0.1fms!", ms)
			w.Write(data)
			return
		}
		log.Printf("Releasing long running request after timing out in %0.1fms!", ms)
		http.Error(w, "Timed out", http.StatusBadRequest)
		return
	}
	data, err := ioutil.ReadAll(proxyResp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("Got away with a proxied request %0.1fms!", time.Now().Sub(proxyAt).Seconds()*1000)
	w.Write(data)
}

func (s *Server) canForward() bool {
	s.forwMutex.Lock()
	defer s.forwMutex.Unlock()
	cnt := s.forwardCnt
	if cnt < 7 {
		s.forwardCnt = cnt + 1
		return true
	}
	return false
}

func (s *Server) decForwardCnt() {
	s.forwMutex.Lock()
	defer s.forwMutex.Unlock()
	s.forwardCnt -= 1
}


func (s *Server) initCluster() {
	log.Printf("Initializing cluster and promoting self to primary")
	for {
		_, err := s.raftServer.Do(&raft.DefaultJoinCommand{
			Name:             s.raftServer.Name(),
			ConnectionString: s.connectionString,
		})
		if err != nil {
			log.Printf("Failed to self promote: '%s'", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}
}

func (s *Server) originalRequestAt(req *http.Request) (at time.Time) {
	atHeader := req.Header.Get("X-Req-At")
	if atHeader == "" {
		at = time.Now()
	} else {
		nanoseconds, _ := strconv.ParseInt(atHeader, 10, 64)
		at = time.Unix(nanoseconds / nsInASecond, nanoseconds % nsInASecond)
		log.Printf("X-Req-At='%s'", at.Format("15:04:05"))
	}
	return at
}

func (s *Server) wasForwarded(req *http.Request) bool {
	return req.Header.Get("X-Req-By") != "" 
}


func (s *Server) originalRequestor(req *http.Request) string {
	res := req.Header.Get("X-Req-By")
	if res != "" {
		return res
	} 
	return s.connectionString
}


func (s *Server) postTimeoutAndWithForwardHeader(cs, path string, requestedAt time.Time, originalCs string, query []byte) (io.Reader, error) {
	testClient :=  &http.Client{
		Transport: &http.Transport{
			Dial: transport.TimeoutDialer(800 * time.Millisecond, 1000 * time.Millisecond)},
	}
	req, err := http.NewRequest("POST", cs + path, bytes.NewBuffer(query))
  	if err != nil {
   		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Req-At", fmt.Sprintf("%d", requestedAt.UnixNano()))
	if originalCs != "" {
		req.Header.Set("X-Req-By", originalCs)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		log.Printf("Aborted proxy request: %s", err)
		return nil, err
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, &transport.RequestError{
			StatusCode: resp.StatusCode,
			Message:    body,
		}
	}

	return bytes.NewBuffer(body), nil
}

func (s *Server) postWithTimeout(
	cs, path string, 
	params interface {},
	conTimeout, rwTimeout time.Duration) error {
	client :=  &http.Client{
		Transport: &http.Transport{
			Dial: transport.TimeoutDialer(conTimeout, rwTimeout)},
	}
	req, err := http.NewRequest("POST", cs + path, util.JSONEncode(params))
  	if err != nil {
   		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
