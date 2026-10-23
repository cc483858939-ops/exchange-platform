package apierrors

// These entries retain reviewed, safe request-validation messages while all
// callers select them by stable codes rather than forwarding error strings.
var validationDefinitions = map[Code]Definition{
	Code("CANNOT_FOLLOW_YOURSELF"):                      {Status: 400, Message: "cannot follow yourself"},
	Code("CANNOT_MUTE_YOURSELF"):                        {Status: 400, Message: "cannot mute yourself"},
	Code("CANNOT_UNFOLLOW_YOURSELF"):                    {Status: 400, Message: "cannot unfollow yourself"},
	Code("EVENTS_MUST_CONTAIN_BETWEEN_1_AND_50_ITEMS"):  {Status: 400, Message: "events must contain between 1 and 50 items"},
	Code("FAILED_TO_OPEN_IMAGE_FILE"):                   {Status: 400, Message: "failed to open image file"},
	Code("FAILED_TO_READ_IMAGE_FILE"):                   {Status: 400, Message: "failed to read image file"},
	Code("IMAGE_FILE_IS_REQUIRED"):                      {Status: 400, Message: "image file is required"},
	Code("IMAGE_FILE_MUST_BE_BETWEEN_1_BYTE_AND_2MB"):   {Status: 400, Message: "image file must be between 1 byte and 2MB"},
	Code("IMAGE_FILE_MUST_BE_BETWEEN_1_BYTE_AND_5MB"):   {Status: 400, Message: "image file must be between 1 byte and 5MB"},
	Code("INVALID_FILE_PATH"):                           {Status: 400, Message: "invalid file path"},
	Code("INVALID_MENTION_PREFIX"):                      {Status: 400, Message: "invalid mention prefix"},
	Code("INVALID_MENTION_SUGGESTION_LIMIT"):            {Status: 400, Message: "invalid mention suggestion limit"},
	Code("INVALID_NOTIFICATION_ID"):                     {Status: 400, Message: "invalid notification id"},
	Code("INVALID_POST_ID"):                             {Status: 400, Message: "Invalid post id"},
	Code("INVALID_POST_VIEW_EVENT_BATCH"):               {Status: 400, Message: "invalid post view event batch"},
	Code("INVALID_QUERY"):                               {Status: 400, Message: "invalid query"},
	Code("INVALID_RECOMMENDATION_EVENT_BATCH"):          {Status: 400, Message: "invalid recommendation event batch"},
	Code("INVALID_TARGET_LANGUAGE"):                     {Status: 400, Message: "invalid target language"},
	Code("INVALID_TRANSLATION_REQUEST"):                 {Status: 400, Message: "invalid translation request"},
	Code("INVALID_USERNAME"):                            {Status: 400, Message: "invalid username"},
	Code("POST_IDS_MUST_CONTAIN_BETWEEN_1_AND_100_IDS"): {Status: 400, Message: "post_ids must contain between 1 and 100 ids"},
	Code("POST_IDS_MUST_CONTAIN_POSITIVE_IDS"):          {Status: 400, Message: "post_ids must contain positive ids"},
	Code("SEARCH_QUERY_IS_REQUIRED"):                    {Status: 400, Message: "search query is required"},
}

func init() {
	for code, definition := range validationDefinitions {
		definitions[code] = definition
	}
}
