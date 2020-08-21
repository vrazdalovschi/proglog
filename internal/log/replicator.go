package log

import (
	"context"
	api "github.com/vrazdalovschi/proglog/api/v1"
	"google.golang.org/grpc"
	"log"
	"sync"
)

type Replicator struct {
	sync.Mutex
	DialOptions []grpc.DialOption
	LocalServer api.LogClient
	servers     map[string]chan struct{}
	closed      bool
	close       chan struct{}
}

func (r *Replicator) Join(_, addr string) error {
	r.Lock()
	defer r.Unlock()
	r.init()
	if r.closed {
		return nil
	}
	if _, ok := r.servers[addr]; ok {
		// already replicating so skip
		return nil
	}
	r.servers[addr] = make(chan struct{})
	go r.replicate(addr, r.servers[addr])
	return nil
}

func (r *Replicator) init() {
	if r.servers == nil {
		r.servers = make(map[string]chan struct{})
	}
	if r.close == nil {
		r.close = make(chan struct{})
	}
}

func (r *Replicator) replicate(addr string, leave chan struct{}) {
	cc, err := grpc.Dial(addr, r.DialOptions...)
	if err != nil {
		r.logErr(err)
		return
	}
	defer cc.Close()
	client := api.NewLogClient(cc)
	ctx := context.Background()
	stream, err := client.ConsumeStream(ctx, &api.ConsumeRequest{Offset: 0})
	if err != nil {
		r.logErr(err)
		return
	}
	records := make(chan *api.Record)
	go func() {
		for {
			recv, err := stream.Recv()
			if err != nil {
				r.logErr(err)
				return
			}
			records <- recv.Record
		}
	}()
	for {
		select {
		case <-r.close:
			return
		case <-leave:
			return
		case record := <-records:
			if _, err = r.LocalServer.Produce(ctx, &api.ProduceRequest{Record: record}); err != nil {
				r.logErr(err)
				return
			}
		}
	}
}
func (r *Replicator) Leave(_, addr string) error {
	r.Lock()
	defer r.Unlock()
	r.init()
	if _, ok := r.servers[addr]; !ok {
		return nil
	}
	close(r.servers[addr])
	delete(r.servers, addr)
	return nil
}

func (r *Replicator) Close() error {
	r.Lock()
	defer r.Unlock()
	r.init()
	if r.closed {
		return nil
	}
	r.closed = true
	close(r.close)
	return nil
}

// TODO expose error channel with all errors, add to clients the possibility to process errors
func (r *Replicator) logErr(err error) {
	log.Printf("[ERROR] proglog: %v", err)
}
