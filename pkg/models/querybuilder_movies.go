package models

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/stashapp/stash/pkg/database"
)

type MovieQueryBuilder struct{}

func NewMovieQueryBuilder() MovieQueryBuilder {
	return MovieQueryBuilder{}
}

func (qb *MovieQueryBuilder) Create(newMovie Movie, tx *sqlx.Tx) (*Movie, error) {
	ensureTx(tx)
	result, err := tx.NamedExec(
		`INSERT INTO movies (checksum, name, aliases, duration, date, rating, studio_id, director, synopsis, url, created_at, updated_at)
				VALUES (:checksum, :name, :aliases, :duration, :date, :rating, :studio_id, :director, :synopsis, :url, :created_at, :updated_at)
		`,
		newMovie,
	)
	if err != nil {
		return nil, err
	}
	movieID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	if err := tx.Get(&newMovie, `SELECT * FROM movies WHERE id = ? LIMIT 1`, movieID); err != nil {
		return nil, err
	}
	return &newMovie, nil
}

func (qb *MovieQueryBuilder) Update(updatedMovie MoviePartial, tx *sqlx.Tx) (*Movie, error) {
	ensureTx(tx)
	_, err := tx.NamedExec(
		`UPDATE movies SET `+SQLGenKeysPartial(updatedMovie)+` WHERE movies.id = :id`,
		updatedMovie,
	)
	if err != nil {
		return nil, err
	}

	return qb.Find(updatedMovie.ID, tx)
}

func (qb *MovieQueryBuilder) UpdateFull(updatedMovie Movie, tx *sqlx.Tx) (*Movie, error) {
	ensureTx(tx)
	_, err := tx.NamedExec(
		`UPDATE movies SET `+SQLGenKeys(updatedMovie)+` WHERE movies.id = :id`,
		updatedMovie,
	)
	if err != nil {
		return nil, err
	}

	return qb.Find(updatedMovie.ID, tx)
}

func (qb *MovieQueryBuilder) Destroy(id string, tx *sqlx.Tx) error {
	// delete movie from movies_scenes

	_, err := tx.Exec("DELETE FROM movies_scenes WHERE movie_id = ?", id)
	if err != nil {
		return err
	}

	// // remove movie from scraped items
	// _, err = tx.Exec("UPDATE scraped_items SET movie_id = null WHERE movie_id = ?", id)
	// if err != nil {
	// 	return err
	// }

	return executeDeleteQuery("movies", id, tx)
}

func (qb *MovieQueryBuilder) Find(id int, tx *sqlx.Tx) (*Movie, error) {
	query := "SELECT * FROM movies WHERE id = ? LIMIT 1"
	args := []interface{}{id}
	return qb.queryMovie(query, args, tx)
}

func (qb *MovieQueryBuilder) FindMany(ids []int) ([]*Movie, error) {
	var movies []*Movie
	for _, id := range ids {
		movie, err := qb.Find(id, nil)
		if err != nil {
			return nil, err
		}

		if movie == nil {
			return nil, fmt.Errorf("movie with id %d not found", id)
		}

		movies = append(movies, movie)
	}

	return movies, nil
}

func (qb *MovieQueryBuilder) FindBySceneID(sceneID int, tx *sqlx.Tx) ([]*Movie, error) {
	query := `
		SELECT movies.* FROM movies
		LEFT JOIN movies_scenes as scenes_join on scenes_join.movie_id = movies.id
		WHERE scenes_join.scene_id = ?
		GROUP BY movies.id
	`
	args := []interface{}{sceneID}
	return qb.queryMovies(query, args, tx)
}

func (qb *MovieQueryBuilder) FindByName(name string, tx *sqlx.Tx, nocase bool) (*Movie, error) {
	query := "SELECT * FROM movies WHERE name = ?"
	if nocase {
		query += " COLLATE NOCASE"
	}
	query += " LIMIT 1"
	args := []interface{}{name}
	return qb.queryMovie(query, args, tx)
}

func (qb *MovieQueryBuilder) FindByNames(names []string, tx *sqlx.Tx, nocase bool) ([]*Movie, error) {
	query := "SELECT * FROM movies WHERE name"
	if nocase {
		query += " COLLATE NOCASE"
	}
	query += " IN " + getInBinding(len(names))
	var args []interface{}
	for _, name := range names {
		args = append(args, name)
	}
	return qb.queryMovies(query, args, tx)
}

func (qb *MovieQueryBuilder) Count() (int, error) {
	return runCountQuery(buildCountQuery("SELECT movies.id FROM movies"), nil)
}

func (qb *MovieQueryBuilder) All() ([]*Movie, error) {
	return qb.queryMovies(selectAll("movies")+qb.getMovieSort(nil), nil, nil)
}

func (qb *MovieQueryBuilder) AllSlim() ([]*Movie, error) {
	return qb.queryMovies("SELECT movies.id, movies.name FROM movies "+qb.getMovieSort(nil), nil, nil)
}

func (qb *MovieQueryBuilder) Query(movieFilter *MovieFilterType, findFilter *FindFilterType) ([]*Movie, int) {
	if findFilter == nil {
		findFilter = &FindFilterType{}
	}
	if movieFilter == nil {
		movieFilter = &MovieFilterType{}
	}

	var whereClauses []string
	var havingClauses []string
	var args []interface{}
	body := selectDistinctIDs("movies")
	body += `
	left join movies_scenes as scenes_join on scenes_join.movie_id = movies.id
	left join scenes on scenes_join.scene_id = scenes.id
	left join studios as studio on studio.id = movies.studio_id
`

	if q := findFilter.Q; q != nil && *q != "" {
		searchColumns := []string{"movies.name"}
		clause, thisArgs := getSearchBinding(searchColumns, *q, false)
		whereClauses = append(whereClauses, clause)
		args = append(args, thisArgs...)
	}

	if studiosFilter := movieFilter.Studios; studiosFilter != nil && len(studiosFilter.Value) > 0 {
		for _, studioID := range studiosFilter.Value {
			args = append(args, studioID)
		}

		whereClause, havingClause := getMultiCriterionClause("movies", "studio", "", "", "studio_id", studiosFilter)
		whereClauses = appendClause(whereClauses, whereClause)
		havingClauses = appendClause(havingClauses, havingClause)
	}

	if isMissingFilter := movieFilter.IsMissing; isMissingFilter != nil && *isMissingFilter != "" {
		switch *isMissingFilter {
		case "front_image":
			body += `left join movies_images on movies_images.movie_id = movies.id
			`
			whereClauses = appendClause(whereClauses, "movies_images.front_image IS NULL")
		case "back_image":
			body += `left join movies_images on movies_images.movie_id = movies.id
			`
			whereClauses = appendClause(whereClauses, "movies_images.back_image IS NULL")
		case "scenes":
			body += `left join movies_scenes on movies_scenes.movie_id = movies.id
			`
			whereClauses = appendClause(whereClauses, "movies_scenes.scene_id IS NULL")
		default:
			whereClauses = appendClause(whereClauses, "movies."+*isMissingFilter+" IS NULL")
		}
	}

	sortAndPagination := qb.getMovieSort(findFilter) + getPagination(findFilter)
	idsResult, countResult := executeFindQuery("movies", body, args, sortAndPagination, whereClauses, havingClauses)

	var movies []*Movie
	for _, id := range idsResult {
		movie, _ := qb.Find(id, nil)
		movies = append(movies, movie)
	}

	return movies, countResult
}

func (qb *MovieQueryBuilder) getMovieSort(findFilter *FindFilterType) string {
	var sort string
	var direction string
	if findFilter == nil {
		sort = "name"
		direction = "ASC"
	} else {
		sort = findFilter.GetSort("name")
		direction = findFilter.GetDirection()
	}

	// #943 - override name sorting to use natural sort
	if sort == "name" {
		return " ORDER BY " + getColumn("movies", sort) + " COLLATE NATURAL_CS " + direction
	}

	return getSort(sort, direction, "movies")
}

func (qb *MovieQueryBuilder) queryMovie(query string, args []interface{}, tx *sqlx.Tx) (*Movie, error) {
	results, err := qb.queryMovies(query, args, tx)
	if err != nil || len(results) < 1 {
		return nil, err
	}
	return results[0], nil
}

func (qb *MovieQueryBuilder) queryMovies(query string, args []interface{}, tx *sqlx.Tx) ([]*Movie, error) {
	var rows *sqlx.Rows
	var err error
	if tx != nil {
		rows, err = tx.Queryx(query, args...)
	} else {
		rows, err = database.DB.Queryx(query, args...)
	}

	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	defer rows.Close()

	movies := make([]*Movie, 0)
	for rows.Next() {
		movie := Movie{}
		if err := rows.StructScan(&movie); err != nil {
			return nil, err
		}
		movies = append(movies, &movie)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return movies, nil
}

func (qb *MovieQueryBuilder) UpdateMovieImages(movieID int, frontImage []byte, backImage []byte, tx *sqlx.Tx) error {
	ensureTx(tx)

	// Delete the existing cover and then create new
	if err := qb.DestroyMovieImages(movieID, tx); err != nil {
		return err
	}

	_, err := tx.Exec(
		`INSERT INTO movies_images (movie_id, front_image, back_image) VALUES (?, ?, ?)`,
		movieID,
		frontImage,
		backImage,
	)

	return err
}

func (qb *MovieQueryBuilder) DestroyMovieImages(movieID int, tx *sqlx.Tx) error {
	ensureTx(tx)

	// Delete the existing joins
	_, err := tx.Exec("DELETE FROM movies_images WHERE movie_id = ?", movieID)
	if err != nil {
		return err
	}
	return err
}

func (qb *MovieQueryBuilder) GetFrontImage(movieID int, tx *sqlx.Tx) ([]byte, error) {
	query := `SELECT front_image from movies_images WHERE movie_id = ?`
	return getImage(tx, query, movieID)
}

func (qb *MovieQueryBuilder) GetBackImage(movieID int, tx *sqlx.Tx) ([]byte, error) {
	query := `SELECT back_image from movies_images WHERE movie_id = ?`
	return getImage(tx, query, movieID)
}
