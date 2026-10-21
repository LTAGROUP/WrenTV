package com.wrentv.tv.player

import android.annotation.SuppressLint
import android.content.Context
import android.view.KeyEvent
import android.webkit.WebChromeClient
import android.webkit.WebView
import okhttp3.HttpUrl

/**
 * Plays a YouTube trailer inside the app through YouTube's IFrame player.
 *
 * YouTube refuses embeds without a real http(s) origin and referrer ("Error
 * 153"), so the page is loaded as if from the server's own address, the way
 * the web client embeds it. A remote's centre and play keys pause and play,
 * left and right skip ten seconds; Back is left to the app.
 */
@SuppressLint("SetJavaScriptEnabled", "ViewConstructor")
class TrailerWebView(context: Context, key: String, server: HttpUrl?) : WebView(context) {
    init {
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.mediaPlaybackRequiresUserGesture = false
        webChromeClient = WebChromeClient()
        setBackgroundColor(android.graphics.Color.BLACK)
        isFocusable = true
        isFocusableInTouchMode = true
        val origin = server?.let { "${it.scheme}://${it.host}${if (it.port != HttpUrl.defaultPort(it.scheme)) ":${it.port}" else ""}" }
            ?: "http://localhost"
        loadDataWithBaseURL("$origin/", html(key, origin), "text/html", "utf-8", null)
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action != KeyEvent.ACTION_DOWN) return super.dispatchKeyEvent(event)
        val js = when (event.keyCode) {
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> "toggle()"
            KeyEvent.KEYCODE_MEDIA_PLAY -> "p&&p.playVideo()"
            KeyEvent.KEYCODE_MEDIA_PAUSE -> "p&&p.pauseVideo()"
            KeyEvent.KEYCODE_DPAD_RIGHT, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> "skip(10)"
            KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_MEDIA_REWIND -> "skip(-10)"
            else -> return super.dispatchKeyEvent(event)
        }
        evaluateJavascript(js, null)
        return true
    }

    /** Stops the sound straight away when the screen closes. */
    fun release() {
        evaluateJavascript("p&&p.stopVideo()", null)
        loadUrl("about:blank")
        destroy()
    }

    private companion object {
        fun html(key: String, origin: String): String {
            val safe = key.filter { it.isLetterOrDigit() || it == '-' || it == '_' }
            return """
                <!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">
                <meta name="referrer" content="strict-origin-when-cross-origin">
                <style>html,body{margin:0;height:100%;background:#000;overflow:hidden}#p{position:fixed;inset:0;width:100%;height:100%}</style>
                </head><body><div id="p"></div>
                <script>
                var p;
                function onYouTubeIframeAPIReady(){
                  p=new YT.Player('p',{videoId:'$safe',host:'https://www.youtube.com',
                    playerVars:{autoplay:1,playsinline:1,rel:0,modestbranding:1,origin:'$origin'},
                    events:{onReady:function(e){e.target.playVideo()}}});
                }
                function toggle(){if(!p)return;p.getPlayerState()==1?p.pauseVideo():p.playVideo()}
                function skip(s){if(p)p.seekTo(Math.max(0,p.getCurrentTime()+s),true)}
                </script>
                <script src="https://www.youtube.com/iframe_api"></script>
                </body></html>
            """.trimIndent()
        }
    }
}
