package server

import (
	"sync"
	"github.com/goraft/raft"
	"stripe-ctf.com/sqlcluster/transport"
	"stripe-ctf.com/sqlcluster/log"
	"fmt"
	"stripe-ctf.com/sqlcluster/util"
	"errors"
	"time"
)

type Command struct {
	Sql string
	ConnectionString string
}


func NewCommand(sql, cs string) *Command {
	return &Command{Sql: sql, ConnectionString: cs}
}
func (c *Command) CommandName() string {
	return "SQL"
}
func (c *Command) Apply(raftServer raft.Server) (interface{}, error) {
	server := raftServer.Context().(*Server)
	
	output, err := server.sql.Execute(raftServer.State(), c.Sql)
	if err != nil {
		var msg string
		if output != nil && len(output.Stderr) > 0 {
			template := `Error executing %#v (%s)

SQLite error: %s`
			msg = fmt.Sprintf(template, c.Sql, err.Error(), util.FmtOutput(output.Stderr))
		} else {
			msg = err.Error()
		}
		output, err = nil, errors.New(msg)
	}

	log.Printf("Just applied sequence number %d (leader is=%s)", 
		output.SequenceNumber, raftServer.Leader())
	formatted := fmt.Sprintf("SequenceNumber: %d\n%s",
		output.SequenceNumber, output.Stdout)
	result := []byte(formatted)


	if "leader" == raftServer.State() && c.ConnectionString != server.connectionString {
		log.Printf("This command was meant to a different leader %s", c.ConnectionString) 
		// this command was meant to a different leader who is
		// still waiting on us.
		go server.Confirm(c.ConnectionString, &Receipt{Sql: c.Sql, Res: result})
	} else if c.ConnectionString == server.connectionString {
		// this command may have been proxied from another
		// thread that had now lost the connection
		server.forwFailed.Rescue(c.Sql, result)
	}

	return result, nil
}

func NewRaftHTTPTransporter(prefix string) *raft.HTTPTransporter {
	t := raft.NewHTTPTransporter(prefix)
	t.Transport.Dial = transport.UnixDialer
	t.Transport.DisableKeepAlives = false
	return t
}


type byteChan chan []byte

// Cheat to recover responses after lengty proxy timeouts. Eventually
// the SQL will replicate onto our local instance, so we can transmit
// the response we did not get trough the proxy.  
// 
// In real life this is stupid as all we care is about the storage
// being consitent and not about satisfying the Octopus
// constraints. Nor it would work elsewhere as only Octopus sends
// random commands.
type FailedProxiedCommands struct {
	waiting  map[string]byteChan
	mutex sync.Mutex
	results  map[string][]byte
}

func NewFailedProxiedCommands() *FailedProxiedCommands {
	return &FailedProxiedCommands{
		waiting: make(map[string]byteChan),
		//since: make(map[string]time.Time,
		results: make(map[string][]byte),
	}
}

func (r *FailedProxiedCommands) Rescue(sql string, response []byte){
	r.mutex.Lock()
	if ping, present := r.waiting[sql]; present == true {
		r.mutex.Unlock()
		r.Clear(sql)
		r.results[sql] = response
		ping <- response
		return
	}
	r.mutex.Unlock()
}

func (r *FailedProxiedCommands) Log() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for sql, _ := range r.waiting {
		log.Printf("Waiting for %s", sql)
	}
}


func (r *FailedProxiedCommands) WaitFor(sql string) (response []byte) {
	r.mutex.Lock()
	if response, present := r.results[sql]; present == true {
		r.mutex.Unlock()
		r.Clear(sql)
		log.Printf("The response had already propagated back to us. sql=%s response=%s", sql, string(response))
		return response
	}

	ping := make(byteChan, 5)
	r.waiting[sql] = ping
	r.mutex.Unlock()

	timeout := time.After(5 * time.Second)
	select {
	case response = <- ping:
		r.Clear(sql)
		return response
	case <- timeout:
		r.Clear(sql)
		return nil
	}
}


func (r *FailedProxiedCommands) ConfirmChan(sql string) byteChan {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	ping := make(byteChan, 5)
	r.waiting[sql] = ping
	return ping
}

func (r *FailedProxiedCommands) Clear(sql string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if ping, present := r.waiting[sql]; present == true {
		if response, present := r.results[sql]; present == true {
			defer func(){
				ping <- response
			}()
		}
	}
	delete(r.waiting, sql)
}
