package controllers

import (
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	ginjson "github.com/gin-gonic/gin/codec/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const onboardingRequestMaxBytes int64 = 16 << 10

type userOnboardingStatusResponse struct {
	Completed     bool       `json:"completed"`
	CurrentStep   string     `json:"current_step"`
	InterestSlugs []string   `json:"interest_slugs"`
	CompletedAt   *time.Time `json:"completed_at"`
}

type updateUserOnboardingProgressRequest struct {
	CurrentStep string `json:"current_step"`
}

type replaceUserOnboardingInterestsRequest struct {
	TopicSlugs *[]string `json:"topic_slugs"`
}

func GetMyOnboarding(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeUserOnboardingError(ctx, http.StatusUnauthorized, "ONBOARDING_AUTH_REQUIRED", "Authentication is required")
		return
	}
	status, err := loadUserOnboardingStatus(ctx, global.APIDb, userID)
	if err != nil {
		writeUserOnboardingStoreError(ctx, userID, err)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func UpdateMyOnboardingProgress(ctx *gin.Context) {
	var request updateUserOnboardingProgressRequest
	if err := bindUserOnboardingJSON(ctx, &request); err != nil {
		writeUserOnboardingError(ctx, requestBodyErrorStatus(err), "ONBOARDING_REQUEST_INVALID", "Invalid onboarding request")
		return
	}
	if !validUserOnboardingStep(request.CurrentStep) {
		writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_STEP_INVALID", "Invalid onboarding step")
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeUserOnboardingError(ctx, http.StatusUnauthorized, "ONBOARDING_AUTH_REQUIRED", "Authentication is required")
		return
	}
	status, err := mutateUserOnboarding(ctx, userID, func(tx *gorm.DB, state *models.UserOnboarding) error {
		if state.CompletedAt != nil {
			return nil
		}
		return tx.Model(state).Update("current_step", request.CurrentStep).Error
	})
	if err != nil {
		writeUserOnboardingStoreError(ctx, userID, err)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func ReplaceMyOnboardingInterests(ctx *gin.Context) {
	var request replaceUserOnboardingInterestsRequest
	if err := bindUserOnboardingJSON(ctx, &request); err != nil {
		writeUserOnboardingError(ctx, requestBodyErrorStatus(err), "ONBOARDING_REQUEST_INVALID", "Invalid onboarding request")
		return
	}
	if request.TopicSlugs == nil {
		writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_INTERESTS_REQUIRED", "topic_slugs is required")
		return
	}
	allowed, err := loadEnabledOnboardingTopicSlugs()
	if err != nil {
		log.Printf("[Onboarding] load curated topics: %v", err)
		writeUserOnboardingError(ctx, http.StatusInternalServerError, "ONBOARDING_TOPICS_UNAVAILABLE", "Onboarding topics are unavailable")
		return
	}
	if len(*request.TopicSlugs) > len(allowed) {
		writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_INTERESTS_INVALID", "Invalid onboarding interests")
		return
	}
	seen := make(map[string]struct{}, len(*request.TopicSlugs))
	for _, slug := range *request.TopicSlugs {
		if slug == "" {
			writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_INTERESTS_INVALID", "Invalid onboarding interests")
			return
		}
		if _, exists := seen[slug]; exists {
			writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_INTERESTS_DUPLICATE", "Duplicate onboarding interest")
			return
		}
		if _, exists := allowed[slug]; !exists {
			writeUserOnboardingError(ctx, http.StatusBadRequest, "ONBOARDING_INTERESTS_INVALID", "Invalid onboarding interests")
			return
		}
		seen[slug] = struct{}{}
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeUserOnboardingError(ctx, http.StatusUnauthorized, "ONBOARDING_AUTH_REQUIRED", "Authentication is required")
		return
	}
	status, err := mutateUserOnboarding(ctx, userID, func(tx *gorm.DB, _ *models.UserOnboarding) error {
		if err := tx.Where("user_id = ?", userID).Delete(&models.UserOnboardingInterest{}).Error; err != nil {
			return err
		}
		if len(*request.TopicSlugs) == 0 {
			return nil
		}
		rows := make([]models.UserOnboardingInterest, 0, len(*request.TopicSlugs))
		for _, slug := range *request.TopicSlugs {
			rows = append(rows, models.UserOnboardingInterest{UserID: userID, TopicSlug: slug})
		}
		return tx.Create(&rows).Error
	})
	if err != nil {
		writeUserOnboardingStoreError(ctx, userID, err)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func CompleteMyOnboarding(ctx *gin.Context) {
	userID, ok := userIDFromContext(ctx)
	if !ok {
		writeUserOnboardingError(ctx, http.StatusUnauthorized, "ONBOARDING_AUTH_REQUIRED", "Authentication is required")
		return
	}
	status, err := mutateUserOnboarding(ctx, userID, func(tx *gorm.DB, state *models.UserOnboarding) error {
		if state.CompletedAt != nil {
			return nil
		}
		now := time.Now().UTC()
		return tx.Model(state).Updates(map[string]any{"completed_at": now, "updated_at": now}).Error
	})
	if err != nil {
		writeUserOnboardingStoreError(ctx, userID, err)
		return
	}
	ctx.JSON(http.StatusOK, status)
}

func loadEnabledOnboardingTopicSlugs() (map[string]struct{}, error) {
	catalog, err := config.LoadCuratedTopics()
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]struct{}, len(catalog.Topics))
	for _, topic := range catalog.Topics {
		if topic.Enabled {
			enabled[topic.Slug] = struct{}{}
		}
	}
	return enabled, nil
}

func validUserOnboardingStep(step string) bool {
	switch step {
	case models.UserOnboardingStepAvatar,
		models.UserOnboardingStepDisplayName,
		models.UserOnboardingStepInterests,
		models.UserOnboardingStepFollowUsers:
		return true
	default:
		return false
	}
}

func bindUserOnboardingJSON(ctx *gin.Context, destination any) error {
	if ctx == nil || ctx.Request == nil || ctx.Request.Body == nil {
		return errors.New("request body is unavailable")
	}
	if err := limitJSONRequest(ctx, onboardingRequestMaxBytes); err != nil {
		return err
	}
	decoder := ginjson.API.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if binding.EnableDecoderUseNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values in onboarding request")
		}
		return err
	}
	return nil
}

func requestBodyErrorStatus(err error) int {
	if isRequestBodyTooLarge(err) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func loadUserOnboardingStatus(ctx *gin.Context, db *gorm.DB, userID uint) (userOnboardingStatusResponse, error) {
	if db == nil || userID == 0 {
		return userOnboardingStatusResponse{}, errors.New("onboarding database is unavailable")
	}
	requestContext := ctx.Request.Context()
	var onboarding models.UserOnboarding
	if err := db.WithContext(requestContext).Where("user_id = ?", userID).Take(&onboarding).Error; err != nil {
		return userOnboardingStatusResponse{}, err
	}
	interests := make([]string, 0)
	if err := db.WithContext(requestContext).Model(&models.UserOnboardingInterest{}).
		Where("user_id = ?", userID).Order("topic_slug ASC").Pluck("topic_slug", &interests).Error; err != nil {
		return userOnboardingStatusResponse{}, err
	}
	sort.Strings(interests)
	return userOnboardingStatusResponse{
		Completed:     onboarding.CompletedAt != nil,
		CurrentStep:   onboarding.CurrentStep,
		InterestSlugs: interests,
		CompletedAt:   onboarding.CompletedAt,
	}, nil
}

func mutateUserOnboarding(ctx *gin.Context, userID uint, mutate func(*gorm.DB, *models.UserOnboarding) error) (userOnboardingStatusResponse, error) {
	if global.APIDb == nil || userID == 0 {
		return userOnboardingStatusResponse{}, errors.New("onboarding database is unavailable")
	}
	var status userOnboardingStatusResponse
	err := global.APIDb.WithContext(ctx.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var state models.UserOnboarding
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&state).Error; err != nil {
			return err
		}
		if err := mutate(tx, &state); err != nil {
			return err
		}
		var err error
		status, err = loadUserOnboardingStatus(ctx, tx, userID)
		return err
	})
	return status, err
}

func writeUserOnboardingStoreError(ctx *gin.Context, userID uint, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("[Onboarding] missing state user=%d", userID)
		writeUserOnboardingError(ctx, http.StatusInternalServerError, "ONBOARDING_STATE_MISSING", "Onboarding state is unavailable")
		return
	}
	log.Printf("[Onboarding] persistence failed user=%d: %v", userID, err)
	writeUserOnboardingError(ctx, http.StatusInternalServerError, "ONBOARDING_UNAVAILABLE", "Onboarding is temporarily unavailable")
}

func writeUserOnboardingError(ctx *gin.Context, status int, code, message string) {
	ctx.JSON(status, gin.H{"code": code, "message": message})
}
