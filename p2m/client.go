package p2m

import (
	"bufio"
	"errors"
	"io"
	"os/user"
	"path/filepath"

	"github.com/dickeyxxx/netrc"
	"github.com/mattermost/platform/model"
)

type Client struct {
	m *model.Client4

	self *model.User
}

func MakeClient(serverURL string) *Client {
	return &Client{
		m: model.NewAPIv4Client(serverURL),
	}
}

func (c *Client) Login() error {
	login, password, err := getUserCredentials()

	if err != nil {
		return err
	}

	user, resp := c.m.Login(login, password)
	if user == nil {
		return resp.Error
	}

	c.self = user

	return nil
}

func (c *Client) GetTeamId() (string, error) {
	teams, resp := c.m.GetAllTeams("", 0, 2)
	if teams == nil {
		return "", resp.Error
	}
	if len(teams) == 1 {
		return teams[0].Id, nil
	}
	return "", errors.New("Multiple teams available")
}

func (c *Client) GetChannelId(name, teamName string) (string, error) {
	var ch *model.Channel
	var resp *model.Response

	var teamId string
	var err error

	if teamName == "" {
		teamId, err = c.GetTeamId()
		if err != nil {
			return "", err
		}
		ch, resp = c.m.GetChannelByName(name, teamId, "")
	} else {
		ch, resp = c.m.GetChannelByNameForTeamName(name, teamName, "")
	}

	if ch == nil {
		return "", resp.Error
	}

	return ch.Id, nil
}

func (c *Client) Post(msg, channelId string) (string, error) {
	draft := model.Post{
		UserId:    c.self.Id,
		ChannelId: channelId,
		Message:   msg,
	}

	p, resp := c.m.CreatePost(&draft)
	if p == nil {
		return "", resp.Error
	}

	return p.Id, nil
}

func (c *Client) Update(postId, msg string) (string, error) {
	draft := model.PostPatch{
		Message: &msg,
	}

	p, resp := c.m.PatchPost(postId, &draft)
	if p == nil {
		return "", resp.Error
	}

	return p.Id, nil
}

func getUserCredentials() (string, string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", "", err
	}

	n, err := netrc.Parse(filepath.Join(usr.HomeDir, ".netrc"))
	if err != nil {
		return "", "", err
	}
	credentials := n.Machine("mattermost")

	return credentials.Get("login"), credentials.Get("password"), nil
}

func (c *Client) Follow(r io.Reader, channelId string, update bool) error {
	scanner := bufio.NewScanner(r)

	var postId string

	doPost := func(msg string) (err error) {
		if update && postId != "" {
			postId, err = c.Update(postId, msg)
		} else {
			postId, err = c.Post(msg, channelId)
		}
		return
	}

	for scanner.Scan() {
		line := scanner.Text()
		if err := doPost(line); err != nil {
			return err
		}
	}

	return scanner.Err()
}
