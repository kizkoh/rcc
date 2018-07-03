package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-redis/redis"
	"github.com/kizkoh/rcc/rcc"
	"github.com/pkg/errors"
)

// debug is extended bool and output debug message
type debug bool

func (debug debug) Printf(f string, v ...interface{}) {
	if debug {
		log.Printf(f, v...)
	}
}

// DEBUG is global debug type
var DEBUG debug

func main() {
	var help = false
	var verbose = false

	// parse args
	flags := flag.NewFlagSet(App.Name, flag.ContinueOnError)

	flags.BoolVar(&verbose, "verbose", verbose, "verbose")
	flags.BoolVar(&help, "h", help, "help")
	flags.BoolVar(&help, "help", help, "help")
	flags.BoolVar(&help, "version", help, "version")

	flags.Usage = func() { usage() }
	if err := flags.Parse(os.Args[1:]); err != nil {
		err = errors.Wrap(err, fmt.Sprintf("%v-%v failed: ", App.Name, App.Version))
		fmt.Printf("%v-%v failed: %v\n", App.Name, App.Version, err)
		os.Exit(1)
	}

	if help {
		usage()
		os.Exit(0)
	}

	DEBUG = debug(verbose)

	args := flags.Args()
	var host string
	if len(args) == 0 {
		host = "127.0.0.1:6379"
	} else if len(args) > 1 {
		usage()
		os.Exit(1)
	} else {
		host = args[0]
	}

	client := redis.NewClient(&redis.Options{
		Addr: host,
	})
	cluster, err := rcc.ClusterNodes(client)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("%v-%v failed: ", App.Name, App.Version))
		fmt.Fprintf(os.Stderr, "%+v", err)
		os.Exit(1)
	}

	statsMemoryInShard := func(node rcc.ClusterNode) (usedMemory string) {
		stat := make(map[string]string)

		client := redis.NewClient(&redis.Options{
			Addr: fmt.Sprintf("%v:%v", node.IP, node.Port),
		})

		res := client.Info("memory").Val()
		for _, line := range strings.Split(res, "\r\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}

			record := strings.SplitN(line, ":", 2)
			if len(record) < 2 {
				continue
			}

			key, value := record[0], record[1]
			stat[key] = value
		}
		return stat["used_memory"]
	}

	statsKeyInShard := func(node rcc.ClusterNode, rank int) (slotStat int, keysStat int, pl PairList) {
		client := redis.NewClient(&redis.Options{
			Addr: fmt.Sprintf("%v:%v", node.IP, node.Port),
		})

		pos := 0

		for _, s := range node.Slots {
			pos = int(s.Start)
			slotStat += int(s.End) - int(s.Start)
			if rank > 0 {
				for pos < int(s.End) {
					cmd := client.ClusterCountKeysInSlot(pos)
					pl = append(pl, Pair{
						Key:   pos,
						Value: cmd.Val(),
					})
				}
			}
		}
		pl = rankBySlotCount(pl)

		keysStat = 0
		expiresStat := 0

		res := client.Info("keyspace").Val()
		for _, line := range strings.Split(res, "\r\n") {
			if strings.HasPrefix(line, "#") {
				continue
			}

			record := strings.SplitN(line, ":", 2)
			if len(record) < 2 {
				continue
			}

			key, value := record[0], record[1]

			if strings.HasPrefix(key, "db") {
				kv := strings.SplitN(value, ",", 3)
				keys, expires := kv[0], kv[1]

				keysStr := strings.SplitN(keys, "=", 2)
				keysv, err := strconv.Atoi(keysStr[1])
				if err != nil {
					// TODO: Error handling
					// logger.Warningf("Failed to parse db keys. %s", err)
					os.Exit(1)
				}
				keysStat += keysv

				expiresStr := strings.SplitN(expires, "=", 2)
				expiresv, err := strconv.Atoi(expiresStr[1])
				if err != nil {
					// TODO: Error handling
					// logger.Warningf("Failed to parse db expires. %s", err)
					os.Exit(1)
				}
				expiresStat += expiresv
			}
		}

		// for _, p := range pl {
		// 	fmt.Printf("%d, %d\n", p.Key, p.Value)
		// }
		return slotStat, keysStat, pl
	}

	// var master rcc.ClusterNode
	for _, node := range cluster {
		for _, flag := range node.Flags {
			if flag == "master" {
				// myself = node
				slotStat, keysStat, _ := statsKeyInShard(node, 0)
				usedMemory := statsMemoryInShard(node)
				fmt.Printf("%s %s:%d ", node.ID, node.Host, node.Port)
				flag := ""
				for i, f := range node.Flags {
					if len(node.Flags)-1 != i {
						flag = fmt.Sprintf("%s%s,", flag, f)
					} else {
						flag = fmt.Sprintf("%s%s", flag, f)
					}
				}

				fmt.Printf("%-16s", "["+flag+"]")
				fmt.Printf("slots:%5d count:%8d avg:%5d ", slotStat, keysStat, keysStat/slotStat)
				fmt.Printf("used_memory:%12s", usedMemory)
				fmt.Print("\n")

			}
		}
	}
}

func rankBySlotCount(pl PairList) PairList {
	sort.Sort(sort.Reverse(pl))
	return pl
}

type Pair struct {
	Key   int
	Value int64
}

type PairList []Pair

func (p PairList) Len() int           { return len(p) }
func (p PairList) Less(i, j int) bool { return p[i].Value < p[j].Value }
func (p PairList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func usage() {
	helpText := `
usage:
   {{.Name}} [command options] <HOST:PORT>

version:
   {{.Version}}

author:
   kizkoh<GitHub: https://github.com/kizkoh>

options:
   --verbose                                    Print verbose messages
   --help, -h                                   Show help
   --version                                    Print the version
`
	t := template.New("usage")
	t, _ = t.Parse(strings.TrimSpace(helpText))
	t.Execute(os.Stdout, App)
	fmt.Println()
}