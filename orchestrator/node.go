/*
Olivier Wulveryck - author of Gorchestrator
Copyright (C) 2015 Olivier Wulveryck

This file is part of the Gorchestrator project and
is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package orchestrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/owulveryck/gorchestrator/structure"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"time"
)

// Node is a "runable" node description
type Node struct {
	ID       int               `json:"id"`
	State    int               `json:"state,omitempty"`
	Name     string            `json:"name,omitempty"`
	Engine   string            `json:"engine,omitempty"` // The execution engine (ie ansible, shell); aim to be like a shebang in a shell file
	Artifact string            `json:"artifact"`
	Args     []string          `json:"args,omitempty"`   // the arguments of the artifact, if needed
	Outputs  map[string]string `json:"output,omitempty"` // the key is the name of the parameter, the value its value (always a string)
}

// Actually executes the node (via the executor)
func (n *Node) Execute(exe ExecutorBackend) error {
	var id string
	var err error
	var t struct {
		ID string `json:"id"`
	}
	url := "https://localhost:8585/v1/tasks"
	b, _ := json.Marshal(*n)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(b))
	//req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	//client := &http.Client{}
	client := exe.Client
	resp, err := client.Do(req)
	if err != nil {
		n.State = NotRunnable
		return err

	}
	defer resp.Body.Close()

	log.Println("Launched")
	if resp.StatusCode >= http.StatusBadRequest {
		n.State = NotRunnable
		return errors.New("Error in the executor")

	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&t); err != nil {
		n.State = Failure
		return err
	}
	id = t.ID

	// Now loop for the result
	for err == nil && n.State < Success {
		var res Node
		fmt.Println("N is", n)
		r, err := http.Get(fmt.Sprintf("%v/%v", url, id))
		if err != nil {
			n.State = NotRunnable
			return err
		}
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&res); err != nil {
			n.State = Failure
			return err
		}
		*n = res
		time.Sleep(1 * time.Second)
	}
	return nil
}

// Run the node
func (n *Node) Run() <-chan Message {
	c := make(chan Message)
	waitForIt := make(chan Graph) // Shared between all messages.
	var ga = regexp.MustCompile(`^get_attribute (.+):(.+)$`)

	var g Graph
	go func() {
		n.State = ToRun
		for n.State <= ToRun {
			c <- Message{n.ID, n.State, waitForIt}
			g = <-waitForIt
			var m structure.Matrix
			m = g.Digraph
			s := m.Dim()
			n.Outputs = make(map[string]string, 0)
			if n.Artifact == "" && n.Engine == "" {
				n.Engine = "nil"
			}
			n.State = Running
			for i := 0; i < s; i++ {
				mu.RLock()
				if m.At(i, n.ID) < Success && m.At(i, n.ID) > 0 {
					n.State = ToRun
				} else if m.At(i, n.ID) >= Failure {
					n.State = NotRunnable
					continue
				}
				mu.RUnlock()
			}
			if n.State == NotRunnable {
				fmt.Printf("I am %v, and I cannot run\n", n.ID)
				c <- Message{n.ID, n.State, waitForIt}
			}
			if n.State == Running {
				// Check and find the arguments
				for i, arg := range n.Args {
					// If argument is a get_attribute node:attribute
					// Then substitute it to its actual value
					subargs := ga.FindStringSubmatch(arg)
					if len(subargs) == 4 {
						nn, _ := g.getNodeFromName(subargs[2])
						n.Args[i] = nn.Outputs[subargs[3]]
					}
				}
				c <- Message{n.ID, n.State, waitForIt}
				switch n.Engine {
				case "nil":
					n.State = Success
				case "sleep": // For test purpose
					time.Sleep(time.Duration(rand.Intn(1e4)) * time.Millisecond)
					rand.Seed(time.Now().Unix())
					n.State = Success
					n.Outputs["result"] = fmt.Sprintf("%v_%v", n.Name, time.Now().Unix())
				default:
					// Send the message to the appropriate backend
					exe := ExecutorBackend{
						"https://localhost:8585/v1",
						"./security/certs/orchestrator/orchestrator.pem",
						"./security/certs/orchestrator/orchestrator_key.pem",
						"./security/certs/executor/executor.pem",
						"/ping",
						nil,
					}
					exe.init()
					err := n.Execute(exe)
					if err != nil {
						n.State = Failure
					}

				}
				c <- Message{n.ID, n.State, waitForIt}
			}
		}
		close(c)
	}()
	return c
}
