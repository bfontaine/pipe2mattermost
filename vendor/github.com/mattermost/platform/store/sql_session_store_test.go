// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package store

import (
	"github.com/mattermost/platform/model"
	"testing"
)

func TestSessionStoreSave(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()

	if err := (<-store.Session().Save(&s1)).Err; err != nil {
		t.Fatal(err)
	}
}

func TestSessionGet(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	s2 := model.Session{}
	s2.UserId = s1.UserId
	Must(store.Session().Save(&s2))

	s3 := model.Session{}
	s3.UserId = s1.UserId
	s3.ExpiresAt = 1
	Must(store.Session().Save(&s3))

	if rs1 := (<-store.Session().Get(s1.Id)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if rs1.Data.(*model.Session).Id != s1.Id {
			t.Fatal("should match")
		}
	}

	if rs2 := (<-store.Session().GetSessions(s1.UserId)); rs2.Err != nil {
		t.Fatal(rs2.Err)
	} else {
		if len(rs2.Data.([]*model.Session)) != 2 {
			t.Fatal("should match len")
		}
	}
}

func TestSessionGetWithDeviceId(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	s1.ExpiresAt = model.GetMillis() + 10000
	Must(store.Session().Save(&s1))

	s2 := model.Session{}
	s2.UserId = s1.UserId
	s2.DeviceId = model.NewId()
	s2.ExpiresAt = model.GetMillis() + 10000
	Must(store.Session().Save(&s2))

	s3 := model.Session{}
	s3.UserId = s1.UserId
	s3.ExpiresAt = 1
	s3.DeviceId = model.NewId()
	Must(store.Session().Save(&s3))

	if rs1 := (<-store.Session().GetSessionsWithActiveDeviceIds(s1.UserId)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if len(rs1.Data.([]*model.Session)) != 1 {
			t.Fatal("should match len")
		}
	}
}

func TestSessionRemove(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().Get(s1.Id)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if rs1.Data.(*model.Session).Id != s1.Id {
			t.Fatal("should match")
		}
	}

	Must(store.Session().Remove(s1.Id))

	if rs2 := (<-store.Session().Get(s1.Id)); rs2.Err == nil {
		t.Fatal("should have been removed")
	}
}

func TestSessionRemoveAll(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().Get(s1.Id)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if rs1.Data.(*model.Session).Id != s1.Id {
			t.Fatal("should match")
		}
	}

	Must(store.Session().RemoveAllSessions())

	if rs2 := (<-store.Session().Get(s1.Id)); rs2.Err == nil {
		t.Fatal("should have been removed")
	}
}

func TestSessionRemoveByUser(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().Get(s1.Id)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if rs1.Data.(*model.Session).Id != s1.Id {
			t.Fatal("should match")
		}
	}

	Must(store.Session().PermanentDeleteSessionsByUser(s1.UserId))

	if rs2 := (<-store.Session().Get(s1.Id)); rs2.Err == nil {
		t.Fatal("should have been removed")
	}
}

func TestSessionRemoveToken(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().Get(s1.Id)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	} else {
		if rs1.Data.(*model.Session).Id != s1.Id {
			t.Fatal("should match")
		}
	}

	Must(store.Session().Remove(s1.Token))

	if rs2 := (<-store.Session().Get(s1.Id)); rs2.Err == nil {
		t.Fatal("should have been removed")
	}

	if rs3 := (<-store.Session().GetSessions(s1.UserId)); rs3.Err != nil {
		t.Fatal(rs3.Err)
	} else {
		if len(rs3.Data.([]*model.Session)) != 0 {
			t.Fatal("should match len")
		}
	}
}

func TestSessionUpdateDeviceId(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().UpdateDeviceId(s1.Id, model.PUSH_NOTIFY_APPLE+":1234567890", s1.ExpiresAt)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	}

	s2 := model.Session{}
	s2.UserId = model.NewId()
	Must(store.Session().Save(&s2))

	if rs2 := (<-store.Session().UpdateDeviceId(s2.Id, model.PUSH_NOTIFY_APPLE+":1234567890", s1.ExpiresAt)); rs2.Err != nil {
		t.Fatal(rs2.Err)
	}
}

func TestSessionUpdateDeviceId2(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if rs1 := (<-store.Session().UpdateDeviceId(s1.Id, model.PUSH_NOTIFY_APPLE_REACT_NATIVE+":1234567890", s1.ExpiresAt)); rs1.Err != nil {
		t.Fatal(rs1.Err)
	}

	s2 := model.Session{}
	s2.UserId = model.NewId()
	Must(store.Session().Save(&s2))

	if rs2 := (<-store.Session().UpdateDeviceId(s2.Id, model.PUSH_NOTIFY_APPLE_REACT_NATIVE+":1234567890", s1.ExpiresAt)); rs2.Err != nil {
		t.Fatal(rs2.Err)
	}
}

func TestSessionStoreUpdateLastActivityAt(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	Must(store.Session().Save(&s1))

	if err := (<-store.Session().UpdateLastActivityAt(s1.Id, 1234567890)).Err; err != nil {
		t.Fatal(err)
	}

	if r1 := <-store.Session().Get(s1.Id); r1.Err != nil {
		t.Fatal(r1.Err)
	} else {
		if r1.Data.(*model.Session).LastActivityAt != 1234567890 {
			t.Fatal("LastActivityAt not updated correctly")
		}
	}

}

func TestSessionCount(t *testing.T) {
	Setup()

	s1 := model.Session{}
	s1.UserId = model.NewId()
	s1.ExpiresAt = model.GetMillis() + 100000
	Must(store.Session().Save(&s1))

	if r1 := <-store.Session().AnalyticsSessionCount(); r1.Err != nil {
		t.Fatal(r1.Err)
	} else {
		if r1.Data.(int64) == 0 {
			t.Fatal("should have at least 1 session")
		}
	}
}
