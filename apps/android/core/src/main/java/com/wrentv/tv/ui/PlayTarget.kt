package com.wrentv.tv.ui

import com.wrentv.tv.data.Item

/** What the player opens. */
data class PlayTarget(
    val itemId: String,
    val title: String,
    val subtitle: String? = null,
    val fromStart: Boolean = false,
    /** A specific version (file) to play; null lets the server choose. */
    val fileId: String? = null,
    /** A downloaded copy on this TV, played without the server. */
    val localFile: String? = null,
    val localPosition: Double? = null,
)

fun Item.playTarget(showTitle: String? = null, fromStart: Boolean = false, fileId: String? = null) = PlayTarget(
    itemId = id,
    title = if (kind == "episode") showTitle ?: this.showTitle ?: title else title,
    subtitle = if (kind == "episode") "$episodeLabel — $title" else null,
    fromStart = fromStart,
    fileId = fileId,
)
