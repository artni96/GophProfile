package handlers

var (
	UnsupportedFormatResponse = []byte(`{"error": "Invalid file format","details":"Supported formats: jpeg, png, webp"}`)
	FileTooLargeResponse      = []byte(`{"error": "File too large","max_size": 10485760}`)
	Response500               = []byte(`{"error": "Internal Server Error"}`)
	Response403               = []byte(`{"error": "Forbidden","details":"You can only delete your own avatars"}`)
)
