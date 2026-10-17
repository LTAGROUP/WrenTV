package com.wrentv.tv

import android.app.SearchManager
import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.provider.BaseColumns
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Answers the TV's own search (the remote's mic, or the search on the home
 * screen) with titles from this server. Picking one opens its details page.
 */
class SearchProvider : ContentProvider() {
    override fun onCreate() = true

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor {
        val cursor = MatrixCursor(COLUMNS)
        val q = (selectionArgs?.firstOrNull() ?: uri.lastPathSegment)?.trim().orEmpty()
        if (q.length < 2 || q == SearchManager.SUGGEST_URI_PATH_QUERY) return cursor
        val app = context?.applicationContext as? WrenTVApplication ?: return cursor
        val items = runBlocking {
            withTimeoutOrNull(4_000) {
                val state = app.state
                // A cold start is still reading the saved sign-in.
                while (state.restoring) delay(100)
                state.api?.search(q)
            }
        }.orEmpty().filter { it.kind == "movie" || it.kind == "show" }.take(20)
        val api = app.state.api
        items.forEachIndexed { i, it ->
            cursor.addRow(arrayOf<Any?>(
                i,
                it.title,
                listOfNotNull(it.year?.toString(), if (it.kind == "show") "TV Show" else "Movie").joinToString(" · "),
                api?.imageUrl(it.posterPath, "w342"),
                if (it.kind == "show") "video/x-tvshow" else "video/x-movie",
                it.year,
                (it.runtimeMin ?: 0) * 60_000L,
                "wrentv://item/${it.id}",
            ))
        }
        return cursor
    }

    override fun getType(uri: Uri): String = SearchManager.SUGGEST_MIME_TYPE
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?) = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?) = 0

    private companion object {
        val COLUMNS = arrayOf(
            BaseColumns._ID,
            SearchManager.SUGGEST_COLUMN_TEXT_1,
            SearchManager.SUGGEST_COLUMN_TEXT_2,
            SearchManager.SUGGEST_COLUMN_RESULT_CARD_IMAGE,
            SearchManager.SUGGEST_COLUMN_CONTENT_TYPE,
            SearchManager.SUGGEST_COLUMN_PRODUCTION_YEAR,
            SearchManager.SUGGEST_COLUMN_DURATION,
            SearchManager.SUGGEST_COLUMN_INTENT_DATA,
        )
    }
}
