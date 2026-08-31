package controllers

import (
	"errors"
	"math"

	"Go.exchange/config"
	"Go.exchange/global"
)

func populateRecommendationAuthorContext(userID uint, profile *userInterestProfile, cfg config.RecommendationConfig) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	profile.AuthorAffinity = make(map[uint]float64)
	profile.FollowingAuthorIDs = make(map[uint]struct{})

	if len(profile.PositiveAffinityContributions) > 0 {
		type postAuthorRow struct {
			PostID uint
			AuthorID  uint
		}
		ids := profile.PositiveAffinityContributionIDs()
		var rows []postAuthorRow
		if err := global.Db.Table("posts").Select("id AS post_id, author_id").Where("id IN ?", ids).Find(&rows).Error; err != nil {
			return err
		}
		raw := make(map[uint]float64)
		for _, row := range rows {
			raw[row.AuthorID] += profile.PositiveAffinityContributions[row.PostID]
		}
		for authorID, value := range raw {
			if cfg.AuthorAffinitySaturationScale > 0 {
				profile.AuthorAffinity[authorID] = clampUnit(math.Tanh(value / cfg.AuthorAffinitySaturationScale))
			}
		}
	}
	var followed []uint
	if err := global.Db.Table("user_follows").Where("follower_id = ?", userID).Pluck("following_id", &followed).Error; err != nil {
		return err
	}
	for _, authorID := range followed {
		if authorID != 0 {
			profile.FollowingAuthorIDs[authorID] = struct{}{}
		}
	}
	return nil
}

func (profile userInterestProfile) PositiveContributionIDs() []uint {
	ids := make([]uint, 0, len(profile.PositiveContributions))
	for postID := range profile.PositiveContributions {
		ids = append(ids, postID)
	}
	return ids
}

func (profile userInterestProfile) PositiveAffinityContributionIDs() []uint {
	ids := make([]uint, 0, len(profile.PositiveAffinityContributions))
	for postID := range profile.PositiveAffinityContributions {
		ids = append(ids, postID)
	}
	return ids
}
