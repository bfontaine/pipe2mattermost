// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/platform/model"
)

func TestPreferenceSave(t *testing.T) {
	Setup()

	id := model.NewId()

	preferences := model.Preferences{
		{
			UserId:   id,
			Category: model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW,
			Name:     model.NewId(),
			Value:    "value1a",
		},
		{
			UserId:   id,
			Category: model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW,
			Name:     model.NewId(),
			Value:    "value1b",
		},
	}
	if count := Must(store.Preference().Save(&preferences)); count != 2 {
		t.Fatal("got incorrect number of rows saved")
	}

	for _, preference := range preferences {
		if data := Must(store.Preference().Get(preference.UserId, preference.Category, preference.Name)).(model.Preference); preference != data {
			t.Fatal("got incorrect preference after first Save")
		}
	}

	preferences[0].Value = "value2a"
	preferences[1].Value = "value2b"
	if count := Must(store.Preference().Save(&preferences)); count != 2 {
		t.Fatal("got incorrect number of rows saved")
	}

	for _, preference := range preferences {
		if data := Must(store.Preference().Get(preference.UserId, preference.Category, preference.Name)).(model.Preference); preference != data {
			t.Fatal("got incorrect preference after second Save")
		}
	}
}

func TestPreferenceGet(t *testing.T) {
	Setup()

	userId := model.NewId()
	category := model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW
	name := model.NewId()

	preferences := model.Preferences{
		{
			UserId:   userId,
			Category: category,
			Name:     name,
		},
		{
			UserId:   userId,
			Category: category,
			Name:     model.NewId(),
		},
		{
			UserId:   userId,
			Category: model.NewId(),
			Name:     name,
		},
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     name,
		},
	}

	Must(store.Preference().Save(&preferences))

	if result := <-store.Preference().Get(userId, category, name); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(model.Preference); data != preferences[0] {
		t.Fatal("got incorrect preference")
	}

	// make sure getting a missing preference fails
	if result := <-store.Preference().Get(model.NewId(), model.NewId(), model.NewId()); result.Err == nil {
		t.Fatal("no error on getting a missing preference")
	}
}

func TestPreferenceGetCategory(t *testing.T) {
	Setup()

	userId := model.NewId()
	category := model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW
	name := model.NewId()

	preferences := model.Preferences{
		{
			UserId:   userId,
			Category: category,
			Name:     name,
		},
		// same user/category, different name
		{
			UserId:   userId,
			Category: category,
			Name:     model.NewId(),
		},
		// same user/name, different category
		{
			UserId:   userId,
			Category: model.NewId(),
			Name:     name,
		},
		// same name/category, different user
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     name,
		},
	}

	Must(store.Preference().Save(&preferences))

	if result := <-store.Preference().GetCategory(userId, category); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(model.Preferences); len(data) != 2 {
		t.Fatal("got the wrong number of preferences")
	} else if !((data[0] == preferences[0] && data[1] == preferences[1]) || (data[0] == preferences[1] && data[1] == preferences[0])) {
		t.Fatal("got incorrect preferences")
	}

	// make sure getting a missing preference category doesn't fail
	if result := <-store.Preference().GetCategory(model.NewId(), model.NewId()); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(model.Preferences); len(data) != 0 {
		t.Fatal("shouldn't have got any preferences")
	}
}

func TestPreferenceGetAll(t *testing.T) {
	Setup()

	userId := model.NewId()
	category := model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW
	name := model.NewId()

	preferences := model.Preferences{
		{
			UserId:   userId,
			Category: category,
			Name:     name,
		},
		// same user/category, different name
		{
			UserId:   userId,
			Category: category,
			Name:     model.NewId(),
		},
		// same user/name, different category
		{
			UserId:   userId,
			Category: model.NewId(),
			Name:     name,
		},
		// same name/category, different user
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     name,
		},
	}

	Must(store.Preference().Save(&preferences))

	if result := <-store.Preference().GetAll(userId); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(model.Preferences); len(data) != 3 {
		t.Fatal("got the wrong number of preferences")
	} else {
		for i := 0; i < 3; i++ {
			if data[0] != preferences[i] && data[1] != preferences[i] && data[2] != preferences[i] {
				t.Fatal("got incorrect preferences")
			}
		}
	}
}

func TestPreferenceDeleteByUser(t *testing.T) {
	Setup()

	userId := model.NewId()
	category := model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW
	name := model.NewId()

	preferences := model.Preferences{
		{
			UserId:   userId,
			Category: category,
			Name:     name,
		},
		// same user/category, different name
		{
			UserId:   userId,
			Category: category,
			Name:     model.NewId(),
		},
		// same user/name, different category
		{
			UserId:   userId,
			Category: model.NewId(),
			Name:     name,
		},
		// same name/category, different user
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     name,
		},
	}

	Must(store.Preference().Save(&preferences))

	if result := <-store.Preference().PermanentDeleteByUser(userId); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestIsFeatureEnabled(t *testing.T) {
	Setup()

	feature1 := "testFeat1"
	feature2 := "testFeat2"
	feature3 := "testFeat3"

	userId := model.NewId()
	category := model.PREFERENCE_CATEGORY_ADVANCED_SETTINGS

	features := model.Preferences{
		{
			UserId:   userId,
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature1,
			Value:    "true",
		},
		{
			UserId:   userId,
			Category: category,
			Name:     model.NewId(),
			Value:    "false",
		},
		{
			UserId:   userId,
			Category: model.NewId(),
			Name:     FEATURE_TOGGLE_PREFIX + feature1,
			Value:    "false",
		},
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature2,
			Value:    "false",
		},
		{
			UserId:   model.NewId(),
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature3,
			Value:    "foobar",
		},
	}

	Must(store.Preference().Save(&features))

	if result := <-store.Preference().IsFeatureEnabled(feature1, userId); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(bool); data != true {
		t.Fatalf("got incorrect setting for feature1, %v=%v", true, data)
	}

	if result := <-store.Preference().IsFeatureEnabled(feature2, userId); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(bool); data != false {
		t.Fatalf("got incorrect setting for feature2, %v=%v", false, data)
	}

	// make sure we get false if something different than "true" or "false" has been saved to database
	if result := <-store.Preference().IsFeatureEnabled(feature3, userId); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(bool); data != false {
		t.Fatalf("got incorrect setting for feature3, %v=%v", false, data)
	}

	// make sure false is returned if a non-existent feature is queried
	if result := <-store.Preference().IsFeatureEnabled("someOtherFeature", userId); result.Err != nil {
		t.Fatal(result.Err)
	} else if data := result.Data.(bool); data != false {
		t.Fatalf("got incorrect setting for non-existent feature 'someOtherFeature', %v=%v", false, data)
	}
}

func TestDeleteUnusedFeatures(t *testing.T) {
	Setup()

	userId1 := model.NewId()
	userId2 := model.NewId()
	category := model.PREFERENCE_CATEGORY_ADVANCED_SETTINGS
	feature1 := "feature1"
	feature2 := "feature2"

	features := model.Preferences{
		{
			UserId:   userId1,
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature1,
			Value:    "true",
		},
		{
			UserId:   userId2,
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature1,
			Value:    "false",
		},
		{
			UserId:   userId1,
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature2,
			Value:    "false",
		},
		{
			UserId:   userId2,
			Category: category,
			Name:     FEATURE_TOGGLE_PREFIX + feature2,
			Value:    "true",
		},
	}

	Must(store.Preference().Save(&features))

	store.Preference().(*SqlPreferenceStore).DeleteUnusedFeatures()

	//make sure features with value "false" have actually been deleted from the database
	if val, err := store.Preference().(*SqlPreferenceStore).GetReplica().SelectInt(`SELECT COUNT(*)
			FROM Preferences
		WHERE Category = :Category
		AND Value = :Val
		AND Name LIKE '`+FEATURE_TOGGLE_PREFIX+`%'`, map[string]interface{}{"Category": model.PREFERENCE_CATEGORY_ADVANCED_SETTINGS, "Val": "false"}); err != nil {
		t.Fatal(err)
	} else if val != 0 {
		t.Fatalf("Found %d features with value 'false', expected all to be deleted", val)
	}
	//
	// make sure features with value "true" remain saved
	if val, err := store.Preference().(*SqlPreferenceStore).GetReplica().SelectInt(`SELECT COUNT(*)
			FROM Preferences
		WHERE Category = :Category
		AND Value = :Val
		AND Name LIKE '`+FEATURE_TOGGLE_PREFIX+`%'`, map[string]interface{}{"Category": model.PREFERENCE_CATEGORY_ADVANCED_SETTINGS, "Val": "true"}); err != nil {
		t.Fatal(err)
	} else if val == 0 {
		t.Fatalf("Found %d features with value 'true', expected to find at least %d features", val, 2)
	}
}

func TestPreferenceDelete(t *testing.T) {
	Setup()

	preference := model.Preference{
		UserId:   model.NewId(),
		Category: model.PREFERENCE_CATEGORY_DIRECT_CHANNEL_SHOW,
		Name:     model.NewId(),
		Value:    "value1a",
	}

	Must(store.Preference().Save(&model.Preferences{preference}))

	if prefs := Must(store.Preference().GetAll(preference.UserId)).(model.Preferences); len([]model.Preference(prefs)) != 1 {
		t.Fatal("should've returned 1 preference")
	}

	if result := <-store.Preference().Delete(preference.UserId, preference.Category, preference.Name); result.Err != nil {
		t.Fatal(result.Err)
	}

	if prefs := Must(store.Preference().GetAll(preference.UserId)).(model.Preferences); len([]model.Preference(prefs)) != 0 {
		t.Fatal("should've returned no preferences")
	}
}

func TestPreferenceDeleteCategory(t *testing.T) {
	Setup()

	category := model.NewId()
	userId := model.NewId()

	preference1 := model.Preference{
		UserId:   userId,
		Category: category,
		Name:     model.NewId(),
		Value:    "value1a",
	}

	preference2 := model.Preference{
		UserId:   userId,
		Category: category,
		Name:     model.NewId(),
		Value:    "value1a",
	}

	Must(store.Preference().Save(&model.Preferences{preference1, preference2}))

	if prefs := Must(store.Preference().GetAll(userId)).(model.Preferences); len([]model.Preference(prefs)) != 2 {
		t.Fatal("should've returned 2 preferences")
	}

	if result := <-store.Preference().DeleteCategory(userId, category); result.Err != nil {
		t.Fatal(result.Err)
	}

	if prefs := Must(store.Preference().GetAll(userId)).(model.Preferences); len([]model.Preference(prefs)) != 0 {
		t.Fatal("should've returned no preferences")
	}
}

func TestPreferenceDeleteCategoryAndName(t *testing.T) {
	Setup()

	category := model.NewId()
	name := model.NewId()
	userId := model.NewId()
	userId2 := model.NewId()

	preference1 := model.Preference{
		UserId:   userId,
		Category: category,
		Name:     name,
		Value:    "value1a",
	}

	preference2 := model.Preference{
		UserId:   userId2,
		Category: category,
		Name:     name,
		Value:    "value1a",
	}

	Must(store.Preference().Save(&model.Preferences{preference1, preference2}))

	if prefs := Must(store.Preference().GetAll(userId)).(model.Preferences); len([]model.Preference(prefs)) != 1 {
		t.Fatal("should've returned 1 preference")
	}

	if prefs := Must(store.Preference().GetAll(userId2)).(model.Preferences); len([]model.Preference(prefs)) != 1 {
		t.Fatal("should've returned 1 preference")
	}

	if result := <-store.Preference().DeleteCategoryAndName(category, name); result.Err != nil {
		t.Fatal(result.Err)
	}

	if prefs := Must(store.Preference().GetAll(userId)).(model.Preferences); len([]model.Preference(prefs)) != 0 {
		t.Fatal("should've returned no preferences")
	}

	if prefs := Must(store.Preference().GetAll(userId2)).(model.Preferences); len([]model.Preference(prefs)) != 0 {
		t.Fatal("should've returned no preferences")
	}
}
