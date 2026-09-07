CREATE TYPE dimensions AS ENUM ('100x100', '300x300');

CREATE TABLE IF NOT EXISTS avatars (
    id UUID PRIMARY KEY NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    file_name VARCHAR(255) NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL,
    s3_key VARCHAR(500),
    upload_status VARCHAR(50) DEFAULT 'pending',
    processing_status VARCHAR(50) DEFAULT 'pending',
    height BIGINT CHECK (height >= 0) NOT NULL,
    width BIGINT CHECK (width >= 0) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS avatar_thumbnails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    avatar_id UUID NOT NULL,
    s3_key VARCHAR(255) NOT NULL,
    dimensions dimensions NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    CONSTRAINT avatar_thumbnail_fk FOREIGN KEY(avatar_id) REFERENCES avatars(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_avatars_user_id ON avatars(user_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_avatars_status ON avatars(upload_status, processing_status);