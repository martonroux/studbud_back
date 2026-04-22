# Study Buddy API Documentation

**Base URL:** `http://localhost:8080`
**Version:** 2.0
**Description:** A REST API for managing study flashcards, subjects, and chapters with sharing, collaboration, and social features.

---

## Table of Contents

- [Authentication](#authentication)
- [Error Responses](#error-responses)
- [Data Models](#data-models)
- [User Endpoints](#user-endpoints)
- [Email Verification Endpoints](#email-verification-endpoints)
- [Image Endpoints](#image-endpoints)
- [Subject Endpoints](#subject-endpoints)
- [Chapter Endpoints](#chapter-endpoints)
- [FlashCard Endpoints](#flashcard-endpoints)
- [Search Endpoints](#search-endpoints)
- [Friendship Endpoints](#friendship-endpoints)
- [Subscription Endpoints](#subscription-endpoints)
- [Collaboration Endpoints](#collaboration-endpoints)
- [Preferences Endpoints](#preferences-endpoints)
- [Gamification Endpoints](#gamification-endpoints)
- [Backend File Structure](#backend-file-structure)

---

## Authentication

Most endpoints require a valid JWT token passed in the `Authorization` header.

**Header format:**
```
Authorization: Bearer <token>
```

**Token details:**
- Algorithm: HS256
- Expiration: 72 hours from issuance
- Claims: `user_id`, `username`, `email_verified`, `exp`

Tokens are obtained via `/user-register` or `/user-login`.

**Email verification enforcement:**
Most protected endpoints require `email_verified: true` in the JWT. Unverified users receive `403 Forbidden` on all routes except `/user-test-jwt` and `/resend-verification`. After verifying email, the user must re-login to get a new token with `email_verified: true`.

---

## Error Responses

All errors follow the same format:

```json
{
  "message": "Description of the error"
}
```

| Status Code | Meaning |
|-------------|---------|
| 400 | Bad Request — invalid input or missing required fields |
| 401 | Unauthorized — missing or invalid JWT token |
| 403 | Forbidden — email not verified, or insufficient permissions |
| 404 | Not Found — resource does not exist or user has no access |
| 500 | Internal Server Error — database or server-side failure |

---

## Data Models

### User
```json
{
  "id": "abcd_efgh",
  "username": "string",
  "email": "user@example.com",
  "emailVerified": true,
  "profilePicture": "http://localhost:8080/images/abcd_efgh",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

### Subject
```json
{
  "id": 1,
  "name": "string",
  "color": "string",
  "icon": "📚",
  "tags": "string",
  "lastUsed": "2024-01-01T00:00:00Z",
  "graphId": 1,
  "archived": false,
  "visibility": "private",
  "flashcardCount": 12,
  "accessLevel": "owner",
  "ownerUsername": "alice"
}
```

| Field | Notes |
|-------|-------|
| `icon` | Optional emoji or short glyph (max 16 chars). Used by the frontend to render a subject icon. Omitted when unset |
| `visibility` | `"private"`, `"friends"`, or `"public"`. Default: `"private"` |
| `flashcardCount` | Total number of flashcards in the subject. Computed at query time |
| `accessLevel` | Present on shared subjects: `"owner"`, `"editor"`, `"viewer"`. Omitted when empty |
| `ownerUsername` | Present on shared/collaborated/subscribed subjects. Omitted when empty |

### Chapter
```json
{
  "id": 1,
  "title": "string",
  "subjectId": 1,
  "flashcardCount": 5
}
```

| Field | Notes |
|-------|-------|
| `flashcardCount` | Number of flashcards in the chapter. Computed at query time on list/search endpoints; `0` on create/update responses |

### FlashCard

```json
{
  "id": 1,
  "title": "string",
  "question": "string",
  "answer": "string",
  "lastResult": -1,
  "lastUsed": "2024-01-01T00:00:00Z",
  "subjectId": 1,
  "chapterId": 1
}
```

`chapterId` is nullable. When `null`, the flashcard belongs directly to the subject without chapter grouping.

**`lastResult` values:**

| Value | Meaning |
|-------|---------|
| -1 | Not yet reviewed |
| 0 | Incorrect |
| 1 | Partial / Uncertain |
| 2 | Correct / Mastered |

### Friendship
```json
{
  "id": 1,
  "senderId": 1,
  "receiverId": 2,
  "status": "pending",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

`status` is one of: `"pending"`, `"accepted"`, `"declined"`.

### Collaborator
```json
{
  "id": 1,
  "subjectId": 1,
  "userId": 2,
  "username": "bob",
  "role": "editor",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

`role` is `"editor"` or `"viewer"`.

### InviteLink
```json
{
  "id": 1,
  "subjectId": 1,
  "token": "abc123def456...",
  "role": "editor",
  "createdBy": 1,
  "expiresAt": "2025-12-31T23:59:59Z",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

`expiresAt` is nullable — `null` means the link never expires.

### Image
```json
{
  "id": "abcd_efgh",
  "url": "http://localhost:8080/images/abcd_efgh"
}
```

Returned from the upload endpoint. Images are served publicly via `GET /images/{id}`.

### Error Response
```json
{
  "message": "string"
}
```

### Achievement
```json
{
  "id": "centurion",
  "title": "Centurion",
  "description": "Master 100 flashcards.",
  "icon": "🏆",
  "category": "mastery",
  "target": 100
}
```

| Field | Notes |
|-------|-------|
| `id` | Stable string identifier (e.g. `"first-steps"`, `"centurion"`). Catalogue is server-defined |
| `category` | `"mastery"`, `"streak"`, `"volume"`, or `"exploration"` |
| `target` | Numeric threshold the user must reach to unlock. Unit depends on category |
| `icon` | Emoji or short glyph used in the UI |

### UnlockedAchievement
```json
{
  "id": "centurion",
  "unlockedAt": "2024-04-15T18:23:11Z"
}
```

Represents a single unlock record for the authenticated user. Response payloads that combine the catalogue with progress merge this with the matching [Achievement](#achievement).

### StreakState
```json
{
  "currentDays": 4,
  "bestDays": 11,
  "lastStudiedDate": "2024-04-16"
}
```

| Field | Notes |
|-------|-------|
| `currentDays` | Current consecutive-day streak |
| `bestDays` | All-time highest consecutive-day streak |
| `lastStudiedDate` | `YYYY-MM-DD` of the last day that counted toward the streak. `null` if the user has never trained |

### DailyGoal
```json
{
  "target": 20,
  "doneToday": 8,
  "date": "2024-04-17"
}
```

| Field | Notes |
|-------|-------|
| `target` | Number of cards the user aims to review per day. Default: `20` |
| `doneToday` | Number of cards reviewed on `date` |
| `date` | `YYYY-MM-DD` the counter is scoped to. The server resets `doneToday` to `0` when the user's local day rolls over |

### UserStats
```json
{
  "masteryPercent": 0.62,
  "cardsStudied": 184,
  "totalCards": 297,
  "goodCount": 132,
  "okCount": 36,
  "badCount": 16,
  "newCount": 113,
  "badgesUnlocked": 4,
  "badgesTotal": 12
}
```

| Field | Notes |
|-------|-------|
| `masteryPercent` | Float in `[0, 1]`. Computed as `(good + ok * 0.5) / total` across all cards the user owns |
| `cardsStudied` | Count of cards with `lastResult != -1` |
| `totalCards` | Total cards in the user's owned subjects |
| `goodCount` / `okCount` / `badCount` / `newCount` | Breakdown matching [FlashCard](#flashcard) `lastResult` values |
| `badgesUnlocked` / `badgesTotal` | Achievement progress summary |

### Preferences
```json
{
  "aiPlanningEnabled": false,
  "dailyGoalTarget": 20
}
```

| Field | Notes |
|-------|-------|
| `aiPlanningEnabled` | Opt-in toggle for AI-driven study planning. Drives whether the frontend shows streak/daily-goal UI. Default: `false` |
| `dailyGoalTarget` | Preferred daily review target. Mirrors [DailyGoal](#dailygoal).`target` and is used to seed it when the AI toggle is first enabled |

### TrainingSession
```json
{
  "id": 42,
  "subjectId": 1,
  "goods": 12,
  "oks": 4,
  "bads": 2,
  "totalCards": 18,
  "completedAt": "2024-04-17T10:31:00Z"
}
```

Represents a completed training session. Returned from [`POST /record-training-session`](#record-a-training-session) and used server-side to update streak, daily goal, and achievements.

---

## Access Model

Subjects have a 3-level visibility: `private`, `friends`, `public`. Access is resolved in this order:

| Level | How granted |
|-------|-------------|
| **owner** | User created the subject |
| **editor** | Added as collaborator with `role: "editor"` |
| **viewer** | Added as collaborator with `role: "viewer"`, or friend of owner (if `visibility: "friends"`), or subscriber (if `visibility: "public"`) |
| **none** | No relationship to the subject |

**Access requirements by operation:**

| Operation | Minimum access |
|-----------|---------------|
| Read subject/chapters/flashcards | viewer |
| Create/update/delete chapters/flashcards | editor |
| Update/delete subject, manage collaborators/invite links | owner |
| Copy subject to own library | viewer |

---

## User Endpoints

### Register a new user

```
POST /user-register
```

Creates a new user account and returns a JWT token. A verification email is sent to the provided email address. The returned token has `email_verified: false` — the user must verify their email before accessing most endpoints.

**Authentication:** None

**Request Body:**
```json
{
  "username": "string",
  "email": "string",
  "password": "string"
}
```

All three fields are required. Email must be a valid format and unique.

**Response `200`:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

**Error Responses:** `400` (invalid input, duplicate username/email), `500`

---

### Login

```
POST /user-login
```

Authenticates a user and returns a JWT token. The `identifier` field accepts either a username or email address.

**Authentication:** None

**Request Body:**
```json
{
  "identifier": "string",
  "password": "string"
}
```

| Field | Notes |
|-------|-------|
| `identifier` | Username or email address |
| `password` | Account password |

**Response `200`:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

**Error Responses:** `400`, `404`, `500`

---

### Validate JWT

```
POST /user-test-jwt
```

Checks whether the stored JWT token is still valid.

**Authentication:** Required

**Request Body:** None

**Response `201`:** No content

**Error Responses:** `401`

---

### Set profile picture

```
POST /set-profile-picture
```

Sets the authenticated user's profile picture to a previously uploaded image. The image must be owned by the user.

**Authentication:** Required (verified)

**Request Body:**
```json
{
  "image_id": "string"
}
```

**Response `200`:**
```json
{
  "message": "profile picture updated"
}
```

**Error Responses:** `400`, `401`, `403`, `404`

---

### Get user stats

```
GET /get-user-stats
```

Returns aggregate mastery and achievement progress across the authenticated user's owned subjects. Used by the home and profile screens to render rings and badge counts. Cards from subjects that are only shared with the user (collaborated, subscribed) are **not** included.

**Authentication:** Required (verified)

**Response `200`:** [UserStats](#userstats)

**Error Responses:** `401`, `403`, `500`

---

## Email Verification Endpoints

### Verify email

```
GET /verify-email
```

Verifies a user's email address using the token from the verification email. This is a public endpoint — the user clicks the link from their email without being logged in.

**Authentication:** None

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `token` | string | Yes | Verification token from the email link |

**Response `200`:**
```json
{
  "message": "email verified successfully"
}
```

**Error Responses:** `400` (invalid/expired token)

---

### Resend verification email

```
POST /resend-verification
```

Resends the verification email to the authenticated user. Rate-limited to one request per 60 seconds.

**Authentication:** Required (unverified users allowed)

**Request Body:** None

**Response `200`:**
```json
{
  "message": "verification email sent"
}
```

**Error Responses:** `400` (already verified, no email set, rate limited), `401`

---

## Image Endpoints

### Upload an image

```
POST /upload-image
```

Uploads an image file. Returns the image ID and public URL. The URL can be embedded in flashcard markdown content as `![](url)`.

**Authentication:** Required (verified)

**Content-Type:** `multipart/form-data`

**Form Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `file` | file | Yes | Image file (max 5MB) |
| `purpose` | string | No | `"general"`, `"profile_picture"`, or `"flashcard"`. Default: `"general"` |

**Allowed MIME types:** `image/jpeg`, `image/png`, `image/gif`, `image/webp` (detected from file content, not header)

**Response `200`:**
```json
{
  "id": "abcd_efgh",
  "url": "http://localhost:8080/images/abcd_efgh"
}
```

**Error Responses:** `400` (invalid type, too large), `401`, `403`

---

### Serve an image

```
GET /images/{imageID}
```

Returns the raw image file. Public endpoint — no authentication required — so images can be loaded in `<img>` tags and markdown renderers.

**Authentication:** None

**Path Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `imageID` | string | Yes | Image ID |

**Response `200`:** Raw image bytes with correct `Content-Type` header. Includes `Cache-Control: public, max-age=86400`.

**Error Responses:** `404`

---

### Delete an image

```
POST /delete-image
```

Deletes an image. Only the image owner can delete it.

**Authentication:** Required (verified)

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | Image ID |

**Response `204`:** No content

**Error Responses:** `400`, `401`, `403`, `404`

---

## Subject Endpoints

### Create a subject

```
POST /create-subject
```

Creates a new subject for the authenticated user. Default visibility is `"private"`.

**Authentication:** Required

**Request Body:**
```json
{
  "name": "string",
  "color": "string",
  "icon": "📚",
  "tags": "string"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `name` | Yes | Unique per user |
| `color` | Yes | |
| `icon` | No | Emoji or short glyph, max 16 chars. Omit or pass empty to leave unset |
| `tags` | No | Comma-separated |

**Response `200`:** [Subject](#subject)

**Error Responses:** `400`, `401`, `500`

---

### Update a subject

```
PUT /update-subject
```

Modifies an existing subject's attributes. Only `id` is mandatory; omitted or empty fields are not updated. Owner only.

**Authentication:** Required

**Request Body:**
```json
{
  "id": "string",
  "name": "string",
  "color": "string",
  "icon": "📚",
  "tags": "string",
  "last_used": "2024-01-01T00:00:00Z",
  "archived": false,
  "visibility": "private"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | Yes | ID of the subject to update |
| `name` | No | |
| `color` | No | |
| `icon` | No | Emoji or short glyph, max 16 chars. Pass an empty string to clear |
| `tags` | No | |
| `last_used` | No | RFC3339 timestamp |
| `archived` | No | Boolean, set to `true` to archive or `false` to unarchive |
| `visibility` | No | `"private"`, `"friends"`, or `"public"` |

**Response `200`:** [Subject](#subject)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Remove a subject

```
POST /remove-subject
```

Deletes a subject and all its associated chapters and flashcards. Owner only.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subject_id` | integer | Yes | ID of the subject to delete |

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get all user subjects

```
GET /get-subjects-user
```

Returns all non-archived subjects the authenticated user has access to: owned subjects, collaborated subjects, and subscribed subjects.

**Authentication:** Required

**Response `200`:** Array of [Subject](#subject) (includes `accessLevel` and `ownerUsername` for non-owned subjects)

**Error Responses:** `401`, `500`

---

### Get archived subjects

```
GET /get-archived-subjects
```

Returns all archived subjects belonging to the authenticated user. Owner-only (no shared subjects).

**Authentication:** Required

**Response `200`:** Array of [Subject](#subject)

**Error Responses:** `401`, `500`

---

### Get subject by ID

```
GET /get-subject
```

Returns a specific subject by its ID. Accessible to any user with at least viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | integer | Yes | ID of the subject |

**Response `200`:** [Subject](#subject) (includes `accessLevel` and `ownerUsername`)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Copy a subject

```
POST /copy-subject
```

Deep-copies a subject (including chapters and flashcards) into the authenticated user's library. Requires at least viewer access on the source subject. The copy is always `"private"` with all `lastResult` reset to `-1`.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string"
}
```

**Response `200`:** [Subject](#subject) (the new copy)

**Error Responses:** `400`, `401`, `404`, `500`

---

## Chapter Endpoints

### Create a chapter

```
POST /create-chapter
```

Creates a new chapter within a subject. Requires editor access.

**Authentication:** Required

**Request Body:**
```json
{
  "title": "string",
  "subject_id": "string"
}
```

**Response `200`:** [Chapter](#chapter)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Update a chapter

```
PUT /update-chapter
```

Updates a chapter's title. Requires editor access.

**Authentication:** Required

**Request Body:**
```json
{
  "id": "string",
  "title": "string"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | Yes | ID of the chapter to update |
| `title` | Yes | New title for the chapter |

**Response `200`:** [Chapter](#chapter)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Remove a chapter

```
POST /remove-chapter
```

Deletes a chapter and all flashcards belonging to it. Requires editor access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `chapter_id` | integer | Yes | ID of the chapter to delete |

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get chapters by subject

```
GET /get-subject-chapters
```

Returns all chapters belonging to a subject. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subject_id` | integer | Yes | ID of the subject |

**Response `200`:** Array of [Chapter](#chapter)

**Error Responses:** `400`, `401`, `404`, `500`

---

## FlashCard Endpoints

### Create a flashcard

```
POST /create-flashcard
```

Creates a new flashcard within a subject, optionally assigned to a chapter. Requires editor access.

**Authentication:** Required

**Request Body:**
```json
{
  "title": "string",
  "question": "string",
  "answer": "string",
  "subject_id": "string",
  "chapter_id": "string"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `title` | Yes | |
| `question` | Yes | Supports markdown content |
| `answer` | Yes | Supports markdown content |
| `subject_id` | Yes | |
| `chapter_id` | No | When omitted, flashcard has no chapter. If provided, the chapter must belong to the specified subject |

**Response `200`:** [FlashCard](#flashcard)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Update a flashcard

```
PUT /update-flashcard
```

Updates a flashcard's data. Fields with empty string values are not updated. Requires editor access.

**Authentication:** Required

**Request Body:**
```json
{
  "id": "string",
  "title": "string",
  "question": "string",
  "answer": "string",
  "subject_id": "string",
  "chapter_id": "string",
  "last_result": "string",
  "last_used": "2024-01-01T00:00:00Z"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | Yes | ID of the flashcard to update |
| `title` | No | |
| `question` | No | Supports markdown content |
| `answer` | No | Supports markdown content |
| `subject_id` | No | |
| `chapter_id` | No | Omitted or empty string = no change. Explicit `null` = unassign from chapter. If set, the chapter must belong to the flashcard's subject |
| `last_result` | No | Value from -1 to 2 |
| `last_used` | No | RFC3339 timestamp |

**Response `200`:** [FlashCard](#flashcard)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Update flashcard result

```
PUT /update-flashcard-result
```

Updates only the `last_result` field of a flashcard. Use this during study sessions to record quiz results. Requires editor access.

**Authentication:** Required

**Request Body:**
```json
{
  "id": "string",
  "last_result": "string"
}
```

| Field | Notes |
|-------|-------|
| `last_result` | Value from -1 to 2 |

**Response `200`:** [FlashCard](#flashcard)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Delete a flashcard

```
POST /delete-flashcard
```

Deletes a flashcard by its ID. Requires editor access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | ID of the flashcard to delete |

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get a flashcard

```
GET /get-flashcard
```

Returns a single flashcard by its ID. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | ID of the flashcard |

**Response `200`:** [FlashCard](#flashcard)

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get flashcard IDs by subject

```
GET /get-subject-flashcards
```

Returns all flashcard IDs belonging to a subject. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | ID of the subject |

**Response `200`:**
```json
{
  "flash_card_ids": [1, 2, 3]
}
```

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get flashcard IDs by chapter

```
GET /get-chapter-flashcards
```

Returns all flashcard IDs belonging to a chapter. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `chapter_id` | string | Yes | ID of the chapter |

**Response `200`:**
```json
{
  "flash_card_ids": [1, 2, 3]
}
```

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get flashcards by difficulty

```
GET /get-flashcards-difficulty
```

Returns flashcard IDs filtered by one or more difficulty levels. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | ID of the subject |
| `chapter_id` | integer | No | Scope results to a specific chapter within the subject |
| `difficulties[]` | integer (CSV) | Yes | Difficulty values to filter by (-1 to 2) |

**Example requests:**
```
GET /get-flashcards-difficulty?id=5&difficulties[]=0&difficulties[]=1
GET /get-flashcards-difficulty?id=5&chapter_id=3&difficulties[]=0&difficulties[]=1
```

**Response `200`:**
```json
{
  "flash_card_ids": [1, 4, 7]
}
```

**Error Responses:** `400`, `401`, `404`, `500`

---

### Get flashcard difficulty counts

```
GET /get-flashcard-results
```

Returns a count of flashcards for each difficulty level in a subject. Requires viewer access.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | ID of the subject |
| `chapter_id` | integer | No | Scope counts to a specific chapter within the subject |

**Response `200`:**
```json
{
  "difficulty_counts": {
    "-1": 5,
    "0": 3,
    "1": 8,
    "2": 12
  }
}
```

**Error Responses:** `400`, `401`, `404`, `500`

---

## Search Endpoints

All search endpoints perform text matching against the specified fields for each entity type. The backend handles tokenization, normalization, and relevance ranking internally.

**Query behavior:**
- Partial word matching is supported (e.g., "eigen" matches "eigenvalue")
- Multiple words in the query are matched independently (e.g., "linear independent" matches content containing both words)
- Special characters common in markdown content (LaTeX backslash commands like `\frac{}`, code fences, URLs) are handled transparently — queries are matched against the raw content
- Results are ordered by relevance

---

### Search subjects

```
GET /search-subjects
```

Performs text search on subject names and tags across all subjects the authenticated user has access to (owned, collaborated, friend-visible, subscribed).

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Search query |

**Response `200`:** Array of matching [Subject](#subject) (includes `ownerUsername`)

**Error Responses:** `400`, `401`, `500`

---

### Search chapters

```
GET /search-chapters
```

Performs text search on chapter titles across all accessible subjects (when no `subject_id` given) or scoped to a specific subject.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Search query |
| `subject_id` | integer | No | Limit results to a specific subject |

**Response `200`:** Array of matching [Chapter](#chapter)

**Error Responses:** `400`, `401`, `500`

---

### Search flashcards

```
GET /search-flashcards
```

Performs text search across flashcard titles, questions, and answers across all accessible subjects (when no filters given) or scoped to a specific subject/chapter.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Search query |
| `subject_id` | integer | No | Limit results to a specific subject |
| `chapter_id` | integer | No | Limit results to a specific chapter |

**Response `200`:** Array of matching [FlashCard](#flashcard)

**Error Responses:** `400`, `401`, `500`

---

### Search public subjects

```
GET /search-public-subjects
```

Performs text search across all subjects with `visibility: "public"`. Not scoped to the authenticated user — this is the global discovery endpoint.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Search query |

**Response `200`:** Array of matching [Subject](#subject) (includes `ownerUsername`)

**Error Responses:** `400`, `401`, `500`

---

### Search users

```
GET /search-users
```

Performs text search on usernames across all users. Used for finding users to add as friends or collaborators.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `q` | string | Yes | Search query |

**Response `200`:** Array of matching [User](#user)

**Error Responses:** `400`, `401`, `500`

---

## Friendship Endpoints

### Send a friend request

```
POST /send-friend-request
```

Sends a friend request to another user. Cannot send to yourself, and cannot duplicate existing requests.

**Authentication:** Required

**Request Body:**
```json
{
  "user_id": "string"
}
```

**Response `200`:** [Friendship](#friendship)

**Error Responses:** `400`, `401`, `404`

---

### Respond to a friend request

```
POST /respond-friend-request
```

Accept or decline a pending friend request. Only the receiver can respond.

**Authentication:** Required

**Request Body:**
```json
{
  "friendship_id": "string",
  "accept": true
}
```

**Response `200`:** [Friendship](#friendship) (status updated to `"accepted"` or `"declined"`)

**Error Responses:** `400`, `401`, `404`

---

### Remove a friend

```
POST /remove-friend
```

Removes a friendship between the authenticated user and the target user. Either party can remove.

**Authentication:** Required

**Request Body:**
```json
{
  "user_id": "string"
}
```

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`

---

### Get friends list

```
GET /get-friends
```

Returns all accepted friends for the authenticated user.

**Authentication:** Required

**Response `200`:** Array of [User](#user)

**Error Responses:** `401`

---

### Get incoming friend requests

```
GET /get-friend-requests
```

Returns pending friend requests received by the authenticated user.

**Authentication:** Required

**Response `200`:** Array of [Friendship](#friendship)

**Error Responses:** `401`

---

### Get sent friend requests

```
GET /get-sent-friend-requests
```

Returns pending friend requests sent by the authenticated user.

**Authentication:** Required

**Response `200`:** Array of [Friendship](#friendship)

**Error Responses:** `401`

---

### Get user by ID

```
GET /get-user-by-id
```

Returns a user's public profile by their ID.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | integer | Yes | User ID |

**Response `200`:** [User](#user)

**Error Responses:** `400`, `401`, `404`

---

## Subscription Endpoints

### Subscribe to a public subject

```
POST /subscribe-subject
```

Subscribes the authenticated user to a public subject. Only works on subjects with `visibility: "public"`. Cannot subscribe to your own subject.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string"
}
```

**Response `200`:**
```json
{
  "id": 1,
  "userId": 1,
  "subjectId": 2,
  "createdAt": "2024-01-01T00:00:00Z"
}
```

**Error Responses:** `400`, `401`, `404`

---

### Unsubscribe from a subject

```
POST /unsubscribe-subject
```

Removes the authenticated user's subscription to a subject.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string"
}
```

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`

---

### Get subscriptions

```
GET /get-subscriptions
```

Returns all subjects the authenticated user is subscribed to, with owner username and access level.

**Authentication:** Required

**Response `200`:** Array of [Subject](#subject) (with `ownerUsername` and `accessLevel: "viewer"`)

**Error Responses:** `401`

---

## Collaboration Endpoints

### Add a collaborator

```
POST /add-collaborator
```

Adds a user as collaborator on a subject. Owner only.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string",
  "user_id": "string",
  "role": "editor"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `role` | Yes | `"editor"` or `"viewer"` |

**Response `200`:** [Collaborator](#collaborator)

**Error Responses:** `400`, `401`, `404`

---

### Remove a collaborator

```
POST /remove-collaborator
```

Removes a collaborator from a subject. Owner only.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string",
  "user_id": "string"
}
```

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`

---

### Update collaborator role

```
PUT /update-collaborator-role
```

Changes a collaborator's role. Owner only.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string",
  "user_id": "string",
  "role": "viewer"
}
```

**Response `200`:** [Collaborator](#collaborator)

**Error Responses:** `400`, `401`, `404`

---

### Get collaborators

```
GET /get-collaborators
```

Lists all collaborators for a subject. Accessible to the owner and any collaborator.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subject_id` | integer | Yes | Subject ID |

**Response `200`:** Array of [Collaborator](#collaborator)

**Error Responses:** `400`, `401`, `404`

---

### Get collaborated subjects

```
GET /get-collaborated-subjects
```

Returns subjects the authenticated user collaborates on (does not include owned subjects).

**Authentication:** Required

**Response `200`:** Array of [Subject](#subject) (with `accessLevel` and `ownerUsername`)

**Error Responses:** `401`

---

### Create an invite link

```
POST /create-invite-link
```

Creates a shareable invite link for a subject. Owner only. The link contains a random 64-character hex token.

**Authentication:** Required

**Request Body:**
```json
{
  "subject_id": "string",
  "role": "editor",
  "expires_at": "2025-12-31T23:59:59Z"
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `role` | Yes | `"editor"` or `"viewer"` |
| `expires_at` | No | RFC3339 timestamp. Omit or `null` for no expiration |

**Response `200`:** [InviteLink](#invitelink)

**Error Responses:** `400`, `401`, `404`

---

### Accept an invite link

```
POST /accept-invite-link
```

Uses an invite token to join as collaborator on a subject. If already a collaborator, updates the role to match the link's role.

**Authentication:** Required

**Request Body:**
```json
{
  "token": "abc123def456..."
}
```

**Response `200`:** [Collaborator](#collaborator)

**Error Responses:** `400`, `401`, `404`

---

### Delete an invite link

```
POST /delete-invite-link
```

Revokes an invite link. Owner only.

**Authentication:** Required

**Request Body:**
```json
{
  "invite_id": "string"
}
```

**Response `204`:** No content

**Error Responses:** `400`, `401`, `404`

---

### Get invite links

```
GET /get-invite-links
```

Lists active invite links for a subject. Owner only.

**Authentication:** Required

**Query Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `subject_id` | integer | Yes | Subject ID |

**Response `200`:** Array of [InviteLink](#invitelink)

**Error Responses:** `400`, `401`, `404`

---

## Preferences Endpoints

User-scoped settings that drive optional UI chrome (AI planning, daily goal target). All preferences are created with server-side defaults on first fetch; clients never need to bootstrap them.

### Get preferences

```
GET /get-preferences
```

Returns the authenticated user's preference set. Missing keys are filled with server defaults on first call and persisted so subsequent reads are stable.

**Authentication:** Required (verified)

**Response `200`:** [Preferences](#preferences)

**Error Responses:** `401`, `403`, `500`

---

### Update preferences

```
PUT /update-preferences
```

Updates one or more preference fields. Omitted fields are left untouched. Changes take effect immediately.

**Authentication:** Required (verified)

**Request Body:**
```json
{
  "aiPlanningEnabled": true,
  "dailyGoalTarget": 25
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `aiPlanningEnabled` | No | When toggled from `false` → `true`, the server initialises (or refreshes) the user's [DailyGoal](#dailygoal) for the current day using `dailyGoalTarget` |
| `dailyGoalTarget` | No | Integer between `1` and `200`. When changed, takes effect on the next day rollover; the current day's target is left as-is |

**Response `200`:** [Preferences](#preferences)

**Error Responses:** `400`, `401`, `403`, `500`

---

## Gamification Endpoints

Gamification state (streaks, daily goals, achievements) is tracked server-side so it persists across devices and reinstalls. The frontend only renders it when [`Preferences.aiPlanningEnabled`](#preferences) is `true`, but the server keeps the data flowing regardless so that toggling AI mode back on reveals accurate state.

### Get gamification state

```
GET /get-gamification-state
```

Returns the current streak, daily goal, and derived session stats for the authenticated user. Safe to poll; cheap on the server.

**Authentication:** Required (verified)

**Response `200`:**
```json
{
  "streak": {
    "currentDays": 4,
    "bestDays": 11,
    "lastStudiedDate": "2024-04-16"
  },
  "dailyGoal": {
    "target": 20,
    "doneToday": 8,
    "date": "2024-04-17"
  },
  "bestSessionGoods": 18
}
```

| Field | Notes |
|-------|-------|
| `streak` | See [StreakState](#streakstate). When the user misses a day, `currentDays` is reset to `0` the first time this endpoint is called after the missed day |
| `dailyGoal` | See [DailyGoal](#dailygoal). `date` always reflects the user's current local day; `doneToday` resets at rollover |
| `bestSessionGoods` | Highest `goods` count recorded in any single [TrainingSession](#trainingsession). Used by achievements like "Perfect Session" |

**Error Responses:** `401`, `403`, `500`

---

### Get achievements

```
GET /get-achievements
```

Returns the full achievement catalogue together with the authenticated user's progress on each entry.

**Authentication:** Required (verified)

**Response `200`:**
```json
{
  "achievements": [
    {
      "achievement": {
        "id": "centurion",
        "title": "Centurion",
        "description": "Master 100 flashcards.",
        "icon": "🏆",
        "category": "mastery",
        "target": 100
      },
      "current": 62,
      "unlocked": false,
      "unlockedAt": null
    }
  ]
}
```

| Field | Notes |
|-------|-------|
| `achievement` | See [Achievement](#achievement) |
| `current` | Progress toward `achievement.target` in the same unit as `target` |
| `unlocked` | `true` once `current >= target` and the unlock has been committed server-side |
| `unlockedAt` | RFC3339 timestamp of the unlock, or `null` if still locked |

**Error Responses:** `401`, `403`, `500`

---

### Record a training session

```
POST /record-training-session
```

Records the result of a completed training session. The server updates the streak, daily goal, and `bestSessionGoods`, then evaluates the achievement catalogue and returns any newly unlocked entries.

Clients should call this endpoint exactly once per session, after [`/update-flashcard-result`](#update-flashcard-result) has been called for every card reviewed.

**Authentication:** Required (verified)

**Request Body:**
```json
{
  "subject_id": 1,
  "goods": 12,
  "oks": 4,
  "bads": 2
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `subject_id` | Yes | Subject the session was scoped to. User must have at least viewer access |
| `goods` | Yes | Number of cards answered correctly in the session |
| `oks` | Yes | Number of partial answers |
| `bads` | Yes | Number of incorrect answers |

`totalCards` is derived on the server as `goods + oks + bads`.

**Response `200`:**
```json
{
  "session": {
    "id": 42,
    "subjectId": 1,
    "goods": 12,
    "oks": 4,
    "bads": 2,
    "totalCards": 18,
    "completedAt": "2024-04-17T10:31:00Z"
  },
  "streak": {
    "currentDays": 5,
    "bestDays": 11,
    "lastStudiedDate": "2024-04-17"
  },
  "dailyGoal": {
    "target": 20,
    "doneToday": 18,
    "date": "2024-04-17"
  },
  "newlyUnlocked": [
    {
      "id": "perfect-session",
      "unlockedAt": "2024-04-17T10:31:00Z"
    }
  ]
}
```

| Field | Notes |
|-------|-------|
| `session` | See [TrainingSession](#trainingsession) |
| `streak` | Updated [StreakState](#streakstate) after this session |
| `dailyGoal` | Updated [DailyGoal](#dailygoal) with `doneToday` incremented by `totalCards` |
| `newlyUnlocked` | Array of [UnlockedAchievement](#unlockedachievement) for achievements that flipped from locked → unlocked as a result of this session. Empty array when nothing unlocked |

**Error Responses:** `400`, `401`, `403`, `404`, `500`

---

### Update daily goal

```
PUT /update-daily-goal
```

Overrides the user's current daily goal target. Unlike the preference field of the same name, this mutates the **active** day's goal immediately — used by the "change today's goal" button in the UI.

**Authentication:** Required (verified)

**Request Body:**
```json
{
  "target": 30
}
```

| Field | Required | Notes |
|-------|----------|-------|
| `target` | Yes | Integer between `1` and `200`. Also written back to [Preferences](#preferences).`dailyGoalTarget` so tomorrow's goal picks up the new value |

**Response `200`:** [DailyGoal](#dailygoal)

**Error Responses:** `400`, `401`, `403`, `500`

---

## Backend File Structure

```
study_buddy_backend/
├── cmd/
│   └── app/
│       └── main.go                        # Application entrypoint, route registration
├── api/
│   ├── handler/
│   │   ├── chapterHandler.go              # Chapter HTTP handlers
│   │   ├── collaborationHandler.go        # Collaboration HTTP handlers
│   │   ├── flashCardHandler.go            # Flash card HTTP handlers
│   │   ├── friendshipHandler.go           # Friendship HTTP handlers
│   │   ├── gamificationHandler.go         # Streak, daily goal, achievements, training sessions
│   │   ├── imageHandler.go               # Image upload/serve/delete handlers
│   │   ├── preferencesHandler.go          # Preferences HTTP handlers (AI toggle, daily goal target)
│   │   ├── searchHandler.go               # Search HTTP handlers
│   │   ├── subjectHandler.go              # Subject HTTP handlers
│   │   ├── subscriptionHandler.go         # Subscription HTTP handlers
│   │   ├── userHandler.go                 # User + email verification + profile + user stats handlers
│   │   ├── types.go                       # Shared request/response types
│   │   └── handlers_test.go              # HTTP-level integration tests
│   └── service/
│       ├── accessService.go               # Centralized access resolution
│       ├── achievementService.go          # Achievement catalogue + progress evaluation
│       ├── chapterService.go              # Chapter business logic
│       ├── collaborationService.go        # Collaboration + invite link logic
│       ├── emailVerificationService.go    # Email verification tokens + verification logic
│       ├── flashCardService.go            # Flash card business logic
│       ├── friendshipService.go           # Friendship business logic
│       ├── gamificationService.go         # Streaks, daily goals, training session recording
│       ├── imageService.go               # Image upload, retrieval, deletion
│       ├── preferencesService.go          # Preferences read/write with defaults
│       ├── searchService.go               # Search business logic
│       ├── subjectService.go              # Subject business logic + copy
│       ├── subscriptionService.go         # Subscription business logic
│       ├── userService.go                 # User business logic (register, login, profile, stats)
│       ├── main_test.go                   # Test setup (TestMain, shared pool)
│       ├── accessService_test.go          # Access resolution tests
│       ├── achievementService_test.go     # Achievement evaluation tests
│       ├── chapterService_test.go         # Chapter tests
│       ├── collaborationService_test.go   # Collaboration tests
│       ├── emailVerificationService_test.go # Email verification tests
│       ├── flashCardService_test.go       # Flash card tests
│       ├── friendshipService_test.go      # Friendship tests
│       ├── gamificationService_test.go    # Streak / daily goal / session tests
│       ├── imageService_test.go           # Image upload/delete tests
│       ├── preferencesService_test.go     # Preferences tests
│       ├── searchService_test.go          # Search tests
│       ├── subjectService_test.go         # Subject + copy tests
│       ├── subscriptionService_test.go    # Subscription tests
│       └── userService_test.go            # User tests
├── internal/
│   ├── db/
│   │   └── hash.go                        # Password hashing utilities
│   ├── email/
│   │   └── email.go                       # SMTP email sending (verification emails)
│   ├── jwt/
│   │   └── jwt.go                         # JWT authentication + middleware + RequireVerified
│   ├── myErrors/
│   │   └── errors.go                      # Custom error definitions (400, 401, 403, 404, 500)
│   └── storage/
│       └── storage.go                     # Local filesystem image storage
├── pkg/
│   ├── achievement/
│   │   └── achievement.go                 # Achievement catalogue entries + UnlockedAchievement model
│   ├── chapter/
│   │   └── chapter.go                     # Chapter model
│   ├── database/
│   │   └── search_setup.go               # Full-text search setup
│   ├── dailyGoal/
│   │   └── dailyGoal.go                   # DailyGoal model + date-rollover helpers
│   ├── flashCard/
│   │   └── flashCard.go                   # Flash card model
│   ├── graph/
│   │   └── graph.go                       # Graph data structure
│   ├── id/
│   │   └── id.go                          # ID generation (NewUserID, NewImageID)
│   ├── preferences/
│   │   └── preferences.go                 # Preferences model (aiPlanningEnabled, dailyGoalTarget)
│   ├── streak/
│   │   └── streak.go                      # StreakState model + streak computation
│   ├── subject/
│   │   └── subject.go                     # Subject model (includes Icon, Visibility, AccessLevel, OwnerUsername)
│   ├── trainingSession/
│   │   └── trainingSession.go             # TrainingSession model
│   └── user/
│       └── user.go                        # User model (includes Email, EmailVerified, ProfilePicture)
├── testutil/
│   ├── testdb.go                          # Test DB connection + cleanup
│   └── fixtures.go                        # Test data factories
├── uploads/                               # Image upload directory (created at startup)
├── db_sql/
│   ├── setup.go                           # Database schema setup (tables, indexes, migrations)
│   └── seed.go                            # Database seed data
├── docs/
│   ├── docs.go                            # Swagger generated docs
│   ├── swagger.json
│   └── swagger.yaml
├── vendor/                                # Go vendored dependencies
├── API.md                                 # API documentation
├── go.mod                                 # Go module definition
├── go.sum                                 # Go dependency checksums
├── launch_app.sh                          # App launch script
└── setup_db.sh                            # Database setup script
```
