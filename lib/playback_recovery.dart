import 'models.dart';

enum PlaybackRecoveryAction { alternative, refresh, stop }

class PlaybackRecovery {
  // 一条失败链里最多尝试的次数：先原线路重新解析 1 次，再逐条换线。
  static const maxAttempts = 6;
  int _attempts = 0;
  bool _refreshed = false;

  void reset() {
    _attempts = 0;
    _refreshed = false;
  }

  PlaybackRecoveryAction next(PlaybackPlan plan) {
    if (_attempts >= maxAttempts) {
      return PlaybackRecoveryAction.stop;
    }
    // 宽容第一层：先在同一线路上重新解析一次。网络抖动、瞬时限流、加载慢
    // 大多重连即恢复，不该一次异常就误判死线路、跳到别的线路。
    if (!_refreshed) {
      _attempts++;
      _refreshed = true;
      return PlaybackRecoveryAction.refresh;
    }
    // 宽容第二层：原线路重试仍失败，才逐条切换到备用线路。
    if (plan.hasAlternative) {
      _attempts++;
      return PlaybackRecoveryAction.alternative;
    }
    return PlaybackRecoveryAction.stop;
  }
}

class PlaybackHealth {
  static const stallTimeout = Duration(seconds: 20);
  Duration? _position;
  DateTime? _lastProgress;

  void reset() {
    _position = null;
    _lastProgress = null;
  }

  bool stalled({
    required Duration position,
    required bool playing,
    required bool foreground,
    required DateTime now,
  }) {
    if (!playing || !foreground || position != _position) {
      _position = position;
      _lastProgress = now;
      return false;
    }
    _lastProgress ??= now;
    return now.difference(_lastProgress!) >= stallTimeout;
  }
}
