package gamification

// achievementDefs is the static catalog of achievements.
// Unlock logic is evaluated after each recorded session.
var achievementDefs = []Achievement{
	{Code: "first_session", Title: "First Steps", Description: "Complete your first training session."},
	{Code: "streak_3", Title: "Three in a Row", Description: "Train three days in a row."},
	{Code: "streak_7", Title: "Week Warrior", Description: "Train seven days in a row."},
	{Code: "streak_30", Title: "Iron Discipline", Description: "Train thirty days in a row."},
	{Code: "cards_100", Title: "Century", Description: "Review 100 cards total."},
	{Code: "cards_1000", Title: "Marathoner", Description: "Review 1000 cards total."},
}

// catalog returns all achievement definitions keyed by code.
func catalog() map[string]Achievement {
	m := make(map[string]Achievement, len(achievementDefs))
	for _, a := range achievementDefs {
		m[a.Code] = a
	}
	return m
}
