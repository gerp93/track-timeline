package database

import (
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// Category is a genre label. Cards reference one by id, but as a soft
// reference rather than a foreign key, so integrity is this package's job:
// creating or editing a card requires a category that exists, and deleting a
// category reassigns its cards first.
type Category struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	Name          string
}

// GetCategories returns every category, alphabetically.
func GetCategories() ([]Category, error) {
	sqlString := `
		SELECT ID, CREATED_ON_DATE, NAME
		FROM TRACK_TIMELINE_CATEGORY
		ORDER BY NAME ASC
	`
	rows, err := query(sqlString)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Category, 0)
	for rows.Next() {
		var category Category
		if err := rows.Scan(&category.Id, &category.CreatedOnDate, &category.Name); err != nil {
			log.Println(err)
			return nil, errors.New("failed to scan row in query results")
		}
		result = append(result, category)
	}

	return result, nil
}

// CategoryExists reports whether a category id is real. Used to enforce the
// soft reference from CARD.CATEGORY_ID before a write.
func CategoryExists(categoryId uuid.UUID) (bool, error) {
	sqlString := "SELECT COUNT(*) FROM TRACK_TIMELINE_CATEGORY WHERE ID = ?"
	rows, err := query(sqlString, categoryId)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return false, errors.New("failed to scan row in query results")
		}
	}

	return count > 0, nil
}

// CreateCategory adds a genre.
func CreateCategory(name string) (uuid.UUID, error) {
	id, err := uuid.NewUUID()
	if err != nil {
		log.Println(err)
		return id, errors.New("failed to generate new id")
	}

	sqlString := "INSERT INTO TRACK_TIMELINE_CATEGORY(ID, NAME) VALUES (?, ?)"
	return id, execute(sqlString, id, name)
}

// CountCardsInCategory reports how many cards would be orphaned by deleting a
// category, so the admin page can say so before asking where to move them.
func CountCardsInCategory(categoryId uuid.UUID) (int, error) {
	sqlString := "SELECT COUNT(*) FROM CARD WHERE CATEGORY_ID = ?"
	rows, err := query(sqlString, categoryId)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		if err := rows.Scan(&count); err != nil {
			log.Println(err)
			return 0, errors.New("failed to scan row in query results")
		}
	}

	return count, nil
}

// DeleteCategoryReassigning moves every card in categoryId over to
// reassignToId, then deletes the category. Reassignment happens first so a
// failure part-way through leaves cards pointing at a category that still
// exists, rather than at a dangling id.
func DeleteCategoryReassigning(categoryId uuid.UUID, reassignToId uuid.UUID) error {
	if categoryId == reassignToId {
		return errors.New("cannot reassign a category to itself")
	}

	exists, err := CategoryExists(reassignToId)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("the category to reassign cards to does not exist")
	}

	if err := execute("UPDATE CARD SET CATEGORY_ID = ? WHERE CATEGORY_ID = ?", reassignToId, categoryId); err != nil {
		return err
	}

	return execute("DELETE FROM TRACK_TIMELINE_CATEGORY WHERE ID = ?", categoryId)
}
