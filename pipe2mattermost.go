package main

import (
	"flag"
	"github.com/bfontaine/pipe2mattermost/p2m"
	"log"
	"os"
)

func main() {
	var team string
	var update bool

	flag.BoolVar(&update, "update", false, "Continuously update the same message")
	flag.StringVar(&team, "team", "", "Team name")

	flag.Parse()

	// echo foo | pipe2mattermost <server URL> <channel>
	serverURL := flag.Arg(0)
	channelSlug := flag.Arg(1)

	if serverURL == "" {
		log.Fatal("I need a server URL")
	}
	if channelSlug == "" {
		log.Fatal("I need a channel slug")
	}

	c := p2m.MakeClient(serverURL)
	if err := c.Login(); err != nil {
		log.Fatal(err)
	}

	channelId, err := c.GetChannelId(channelSlug, team)

	if err != nil {
		log.Fatal(err)
	}

	if err := c.Follow(os.Stdin, channelId, update); err != nil {
		log.Fatal(err)
	}
}
