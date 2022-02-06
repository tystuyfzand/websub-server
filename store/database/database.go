package database

import (
	"database/sql"
	"meow.tf/websub/handler"
	"meow.tf/websub/model"
	"meow.tf/websub/store"
	"time"
)

// New creates a new database store.
func New(db *sql.DB) *Store {
	s := &Store{
		Handler: handler.New(),
		db:      db,
	}

	go func() {
		t := time.NewTicker(60 * time.Second)
		for {
			<-t.C

			s.Cleanup()
		}
	}()

	return s
}

// Store represents a database backed store.
type Store struct {
	*handler.Handler
	db *sql.DB
}

// Cleanup will run a query to remove expired subscriptions, as well as clean up topics.
func (s *Store) Cleanup() {
	// Cleanup expired subscriptions which were not renewed
	_, err := s.db.Exec("DELETE FROM subscriptions WHERE expires_at <= NOW()")

	if err != nil {
		return
	}

	// Cleanup topics with no subscriptions
	_, err = s.db.Exec("DELETE FROM topics WHERE not exists (select 1 from subscriptions where subscriptions.topic_id = topics.id)")

	if err != nil {
		return
	}
}

// All retrieves all active subscriptions for a topic.
func (s *Store) All(topic string) ([]model.Subscription, error) {
	topicRow := s.db.QueryRow("SELECT id FROM topics WHERE topic = ?", topic)

	var topicID int64

	if err := topicRow.Scan(&topicID); err != nil {
		return nil, store.ErrNotFound
	}

	rows, err := s.db.Query("SELECT id, callback, secret, lease, expires_at FROM subscriptions WHERE topic_id = ?", topicID)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	subscriptions := make([]model.Subscription, 0)

	for rows.Next() {
		sub := model.Subscription{
			Topic: topic,
		}

		var leaseSeconds int

		if err := rows.Scan(&sub.ID, &sub.Callback, &sub.Secret, &leaseSeconds, &sub.Expires); err != nil {
			return nil, err
		}

		sub.LeaseTime = time.Duration(leaseSeconds) * time.Second

		subscriptions = append(subscriptions, sub)
	}

	return subscriptions, nil
}

// findTopic will find an existing topic and return the id.
func (s *Store) findTopic(topic string) (int64, error) {
	topicRow := s.db.QueryRow("SELECT id FROM topics WHERE topic = ?", topic)

	var topicID int64

	if err := topicRow.Scan(&topicID); err != nil {
		if err == sql.ErrNoRows {
			return -1, store.ErrNotFound
		}

		return -1, err
	}

	return topicID, nil
}

// findOrCreateTopic will find an existing topic, or create a new topic and return the id.
func (s *Store) findOrCreateTopic(topic string) (int64, error) {
	topicID, err := s.findTopic(topic)

	if err == nil {
		return topicID, nil
	}

	topicRes, err := s.db.Exec("INSERT INTO topics (`topic`) VALUES (?)", topic)

	if err != nil {
		return -1, err
	}

	topicID, err = topicRes.LastInsertId()

	if err != nil {
		return -1, err
	}

	return topicID, nil
}

// Add stores a subscription in the bucket for the specified topic.
func (s *Store) Add(sub model.Subscription) error {
	topicID, err := s.findOrCreateTopic(sub.Topic)

	if err != nil {
		return err
	}

	res, err := s.db.Exec("INSERT INTO subscriptions(`topic_id`, `callback`, `secret`, `lease`, `expires_at`) VALUES (?, ?, ?, ?, ?)",
		topicID, sub.Callback, sub.Secret, sub.LeaseTime/time.Second, sub.Expires)

	if err != nil {
		return err
	}

	sub.ID, err = res.LastInsertId()

	if err != nil {
		return err
	}

	s.Call(&store.Added{Subscription: sub})
	return nil
}

// Get retrieves a subscription for the specified topic and callback.
func (s *Store) Get(topic, callback string) (*model.Subscription, error) {
	topicID, err := s.findTopic(topic)

	if err != nil {
		return nil, err
	}

	row := s.db.QueryRow("SELECT id, callback, secret, lease, expires_at FROM subscriptions WHERE topic_id = ? AND callback = ?", topicID, callback)

	sub := model.Subscription{
		Topic: topic,
	}

	var leaseSeconds int

	if err := row.Scan(&sub.ID, &sub.Callback, &sub.Secret, &leaseSeconds, &sub.Expires); err != nil {
		return nil, err
	}

	sub.LeaseTime = time.Duration(leaseSeconds) * time.Second

	return &sub, nil
}

// Remove removes a subscription from the database for the specified topic and callback.
func (s *Store) Remove(sub model.Subscription) error {
	if sub.ID > 0 {
		_, err := s.db.Exec("DELETE FROM subscriptions WHERE id = ?", sub.ID)

		return err
	}

	topicID, err := s.findTopic(sub.Topic)

	if err != nil {
		return err
	}

	_, err = s.db.Exec("DELETE FROM subscriptions WHERE topic_id = ? AND callback = ?", topicID, sub.Callback)

	if err != nil {
		return err
	}

	s.Call(&store.Removed{Subscription: sub})
	return nil
}
