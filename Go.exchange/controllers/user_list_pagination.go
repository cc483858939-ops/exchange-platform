package controllers

// Offset pagination is supported only through this start position. Keep this
// separate from per-page limits, and stop advertising pages beyond the bound.
const maxUserListOffset = 10_000
