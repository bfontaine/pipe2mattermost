package app

import (
	"testing"

	"github.com/mattermost/platform/model"
	"github.com/stretchr/testify/assert"
)

func TestRenameProviderDoCommand(t *testing.T) {
	th := Setup().InitBasic()

	rp := RenameProvider{}
	args := &model.CommandArgs{
		T:         func(s string, args ...interface{}) string { return s },
		ChannelId: th.BasicChannel.Id,
		Session:   model.Session{UserId: th.BasicUser.Id, TeamMembers: []*model.TeamMember{&model.TeamMember{TeamId: th.BasicTeam.Id, Roles: model.ROLE_TEAM_USER.Id}}},
	}

	// Blank text is a success
	for msg, expected := range map[string]string{
		"":                        "api.command_channel_rename.message.app_error",
		"o":                       "api.command_channel_rename.too_short.app_error",
		"joram":                   "",
		"1234567890123456789012":  "",
		"12345678901234567890123": "api.command_channel_rename.too_long.app_error",
	} {
		actual := rp.DoCommand(args, msg).Text
		assert.Equal(t, expected, actual)
	}
}
