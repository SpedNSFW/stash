package api

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/stashapp/stash/pkg/database"
	"github.com/stashapp/stash/pkg/models"
	"github.com/stashapp/stash/pkg/utils"
)

func (r *mutationResolver) MovieCreate(ctx context.Context, input models.MovieCreateInput) (*models.Movie, error) {
	// generate checksum from movie name rather than image
	checksum := utils.MD5FromString(input.Name)

	var frontimageData []byte
	var backimageData []byte
	var err error

	// HACK: if back image is being set, set the front image to the default.
	// This is because we can't have a null front image with a non-null back image.
	if input.FrontImage == nil && input.BackImage != nil {
		input.FrontImage = &models.DefaultMovieImage
	}

	// Process the base 64 encoded image string
	if input.FrontImage != nil {
		_, frontimageData, err = utils.ProcessBase64Image(*input.FrontImage)
		if err != nil {
			return nil, err
		}
	}

	// Process the base 64 encoded image string
	if input.BackImage != nil {
		_, backimageData, err = utils.ProcessBase64Image(*input.BackImage)
		if err != nil {
			return nil, err
		}
	}

	// Populate a new movie from the input
	currentTime := time.Now()
	newMovie := models.Movie{
		Checksum:  checksum,
		Name:      sql.NullString{String: input.Name, Valid: true},
		CreatedAt: models.SQLiteTimestamp{Timestamp: currentTime},
		UpdatedAt: models.SQLiteTimestamp{Timestamp: currentTime},
	}

	if input.Aliases != nil {
		newMovie.Aliases = sql.NullString{String: *input.Aliases, Valid: true}
	}
	if input.Duration != nil {
		duration := int64(*input.Duration)
		newMovie.Duration = sql.NullInt64{Int64: duration, Valid: true}
	}

	if input.Date != nil {
		newMovie.Date = models.SQLiteDate{String: *input.Date, Valid: true}
	}

	if input.Rating != nil {
		rating := int64(*input.Rating)
		newMovie.Rating = sql.NullInt64{Int64: rating, Valid: true}
	}

	if input.StudioID != nil {
		studioID, _ := strconv.ParseInt(*input.StudioID, 10, 64)
		newMovie.StudioID = sql.NullInt64{Int64: studioID, Valid: true}
	}

	if input.Director != nil {
		newMovie.Director = sql.NullString{String: *input.Director, Valid: true}
	}

	if input.Synopsis != nil {
		newMovie.Synopsis = sql.NullString{String: *input.Synopsis, Valid: true}
	}

	if input.URL != nil {
		newMovie.URL = sql.NullString{String: *input.URL, Valid: true}
	}

	// Start the transaction and save the movie
	tx := database.DB.MustBeginTx(ctx, nil)
	qb := models.NewMovieQueryBuilder()
	movie, err := qb.Create(newMovie, tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	// update image table
	if len(frontimageData) > 0 {
		if err := qb.UpdateMovieImages(movie.ID, frontimageData, backimageData, tx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}

	// Commit
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return movie, nil
}

func (r *mutationResolver) MovieUpdate(ctx context.Context, input models.MovieUpdateInput) (*models.Movie, error) {
	// Populate movie from the input
	movieID, _ := strconv.Atoi(input.ID)

	updatedMovie := models.MoviePartial{
		ID:        movieID,
		UpdatedAt: &models.SQLiteTimestamp{Timestamp: time.Now()},
	}

	translator := changesetTranslator{
		inputMap: getUpdateInputMap(ctx),
	}

	var frontimageData []byte
	var err error
	frontImageIncluded := translator.hasField("front_image")
	if input.FrontImage != nil {
		_, frontimageData, err = utils.ProcessBase64Image(*input.FrontImage)
		if err != nil {
			return nil, err
		}
	}
	backImageIncluded := translator.hasField("back_image")
	var backimageData []byte
	if input.BackImage != nil {
		_, backimageData, err = utils.ProcessBase64Image(*input.BackImage)
		if err != nil {
			return nil, err
		}
	}

	if input.Name != nil {
		// generate checksum from movie name rather than image
		checksum := utils.MD5FromString(*input.Name)
		updatedMovie.Name = &sql.NullString{String: *input.Name, Valid: true}
		updatedMovie.Checksum = &checksum
	}

	updatedMovie.Aliases = translator.nullString(input.Aliases, "aliases")
	updatedMovie.Duration = translator.nullInt64(input.Duration, "duration")
	updatedMovie.Date = translator.sqliteDate(input.Date, "date")
	updatedMovie.Rating = translator.nullInt64(input.Rating, "rating")
	updatedMovie.StudioID = translator.nullInt64FromString(input.StudioID, "studio_id")
	updatedMovie.Director = translator.nullString(input.Director, "director")
	updatedMovie.Synopsis = translator.nullString(input.Synopsis, "synopsis")
	updatedMovie.URL = translator.nullString(input.URL, "url")

	// Start the transaction and save the movie
	tx := database.DB.MustBeginTx(ctx, nil)
	qb := models.NewMovieQueryBuilder()
	movie, err := qb.Update(updatedMovie, tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	// update image table
	if frontImageIncluded || backImageIncluded {
		if !frontImageIncluded {
			frontimageData, err = qb.GetFrontImage(updatedMovie.ID, tx)
			if err != nil {
				tx.Rollback()
				return nil, err
			}
		}
		if !backImageIncluded {
			backimageData, err = qb.GetBackImage(updatedMovie.ID, tx)
			if err != nil {
				tx.Rollback()
				return nil, err
			}
		}

		if len(frontimageData) == 0 && len(backimageData) == 0 {
			// both images are being nulled. Destroy them.
			if err := qb.DestroyMovieImages(movie.ID, tx); err != nil {
				tx.Rollback()
				return nil, err
			}
		} else {
			// HACK - if front image is null and back image is not null, then set the front image
			// to the default image since we can't have a null front image and a non-null back image
			if frontimageData == nil && backimageData != nil {
				_, frontimageData, _ = utils.ProcessBase64Image(models.DefaultMovieImage)
			}

			if err := qb.UpdateMovieImages(movie.ID, frontimageData, backimageData, tx); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
		}
	}

	// Commit
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return movie, nil
}

func (r *mutationResolver) MovieDestroy(ctx context.Context, input models.MovieDestroyInput) (bool, error) {
	qb := models.NewMovieQueryBuilder()
	tx := database.DB.MustBeginTx(ctx, nil)
	if err := qb.Destroy(input.ID, tx); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *mutationResolver) MoviesDestroy(ctx context.Context, ids []string) (bool, error) {
	qb := models.NewMovieQueryBuilder()
	tx := database.DB.MustBeginTx(ctx, nil)
	for _, id := range ids {
		if err := qb.Destroy(id, tx); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
