-- name: CreateCategory :one
INSERT INTO categories (id, user_id, name, emoji, kind, archived)
VALUES (@id, @user_id, @name, @emoji, @kind, @archived)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = @id AND user_id = @user_id;

-- name: ListCategories :many
SELECT * FROM categories WHERE user_id = @user_id ORDER BY created_at, name;

-- name: UpdateCategory :one
UPDATE categories SET name = @name, emoji = @emoji, kind = @kind, archived = @archived, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: CategoryInUse :one
SELECT EXISTS (SELECT 1 FROM expenses e WHERE e.user_id = @user_id AND e.category_id = @id)
    OR EXISTS (SELECT 1 FROM planned_payments p WHERE p.user_id = @user_id AND p.category_id = @id)
    OR EXISTS (SELECT 1 FROM income i WHERE i.user_id = @user_id AND i.category_id = @id)
    OR EXISTS (SELECT 1 FROM recurring_transactions r WHERE r.user_id = @user_id AND r.category_id = @id) AS in_use;

-- name: DeleteCategory :execrows
DELETE FROM categories WHERE id = @id AND user_id = @user_id;
