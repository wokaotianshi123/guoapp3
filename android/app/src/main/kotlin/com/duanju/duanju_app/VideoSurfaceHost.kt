package com.duanjuzyz.duanju_app

import android.content.Context
import android.view.Surface
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import io.flutter.embedding.engine.plugins.FlutterPlugin
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.StandardMessageCodec
import io.flutter.plugin.platform.PlatformView
import io.flutter.plugin.platform.PlatformViewFactory

const val VIDEO_SURFACE_VIEW_TYPE = "duanju/video_surface"

internal object MediaKitSurfaceRefs {
    private val helperClass: Class<*>? by lazy {
        runCatching {
            Class.forName("com.alexmercerind.mediakitandroidhelper.MediaKitAndroidHelper")
        }.getOrNull()
    }

    private val newRef: java.lang.reflect.Method? by lazy {
        runCatching {
            helperClass?.getDeclaredMethod("newGlobalObjectRef", Object::class.java)
                ?.apply { isAccessible = true }
        }.getOrNull()
    }

    private val deleteRef: java.lang.reflect.Method? by lazy {
        runCatching {
            helperClass?.getDeclaredMethod("deleteGlobalObjectRef", java.lang.Long.TYPE)
                ?.apply { isAccessible = true }
        }.getOrNull()
    }

    val available: Boolean
        get() = newRef != null && deleteRef != null

    fun create(surface: Surface): Long {
        val method = newRef ?: return 0L
        return runCatching { method.invoke(null, surface) as? Long ?: 0L }.getOrDefault(0L)
    }

    fun release(reference: Long) {
        if (reference == 0L) return
        val method = deleteRef ?: return
        runCatching { method.invoke(null, reference) }
    }
}

internal class VideoSurfaceHost(
    context: Context,
    messenger: BinaryMessenger,
    viewId: Int
) : PlatformView, SurfaceHolder.Callback, View.OnLayoutChangeListener {

    private val channel = MethodChannel(messenger, "$VIDEO_SURFACE_VIEW_TYPE/$viewId")
    private var reference = 0L
    private var released = false
    private var lastWidth = 0
    private var lastHeight = 0
    private val releaseHandler = android.os.Handler(android.os.Looper.getMainLooper())

    private fun scheduleRelease(target: Long) {
        if (target == 0L) return
        releaseHandler.postDelayed(
            Runnable { MediaKitSurfaceRefs.release(target) },
            RELEASE_DELAY_MILLIS
        )
    }

    private val surfaceView = SurfaceView(context).apply {
        holder.setFormat(android.graphics.PixelFormat.OPAQUE)
        holder.addCallback(this@VideoSurfaceHost)
        setZOrderMediaOverlay(true)
        isClickable = false
        isFocusable = false
        isFocusableInTouchMode = false
        addOnLayoutChangeListener(this@VideoSurfaceHost)
    }

    override fun getView(): View = surfaceView

    override fun onFlutterViewAttached(flutterView: View) {
        surfaceView.visibility = View.VISIBLE
    }

    override fun onFlutterViewDetached() {
        surfaceView.visibility = View.GONE
    }

    private fun reportSize(width: Int, height: Int) {
        if (released || reference == 0L) return
        if (width <= 0 || height <= 0) return
        if (width == lastWidth && height == lastHeight) return
        lastWidth = width
        lastHeight = height
        channel.invokeMethod(
            "surfaceChanged",
            mapOf("width" to width, "height" to height)
        )
    }

    override fun surfaceCreated(holder: SurfaceHolder) {
        if (released) return
        val previous = reference
        val created = MediaKitSurfaceRefs.create(holder.surface)
        if (created == 0L) {
            scheduleRelease(previous)
            reference = 0L
            channel.invokeMethod("surfaceUnavailable", null)
            return
        }
        reference = created
        scheduleRelease(previous)
        lastWidth = 0
        lastHeight = 0
        channel.invokeMethod(
            "surfaceCreated",
            mapOf(
                "wid" to reference,
                "width" to surfaceView.width,
                "height" to surfaceView.height
            )
        )
        reportSize(surfaceView.width, surfaceView.height)
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        reportSize(width, height)
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        lastWidth = 0
        lastHeight = 0
        if (reference == 0L) return
        channel.invokeMethod("surfaceDestroyed", null)
        scheduleRelease(reference)
        reference = 0L
    }

    override fun onLayoutChange(
        view: View,
        left: Int,
        top: Int,
        right: Int,
        bottom: Int,
        oldLeft: Int,
        oldTop: Int,
        oldRight: Int,
        oldBottom: Int
    ) {
        reportSize(right - left, bottom - top)
    }

    override fun dispose() {
        if (released) return
        released = true
        surfaceView.removeOnLayoutChangeListener(this)
        surfaceView.holder.removeCallback(this)
        channel.setMethodCallHandler(null)
        scheduleRelease(reference)
        reference = 0L
    }

    private companion object {
        const val RELEASE_DELAY_MILLIS = 5000L
    }
}

internal class VideoSurfaceFactory(
    private val messenger: BinaryMessenger
) : PlatformViewFactory(StandardMessageCodec.INSTANCE) {
    override fun create(context: Context, viewId: Int, args: Any?): PlatformView {
        return VideoSurfaceHost(context, messenger, viewId)
    }
}

internal class VideoSurfacePlugin : FlutterPlugin {
    override fun onAttachedToEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        binding.platformViewRegistry.registerViewFactory(
            VIDEO_SURFACE_VIEW_TYPE,
            VideoSurfaceFactory(binding.binaryMessenger)
        )
    }

    override fun onDetachedFromEngine(binding: FlutterPlugin.FlutterPluginBinding) = Unit
}
