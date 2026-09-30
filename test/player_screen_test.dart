import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/player_screen.dart';
import 'package:duanju_app/app_layout.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';
import 'player_fixtures.dart';

void main() {
  Future<void> settleOperations(WidgetTester tester) async {
    for (var i = 0; i < 12; i++) {
      await tester.pump(const Duration(milliseconds: 10));
    }
  }

  Future<void> mount(
    WidgetTester tester,
    RouteRepository repository,
    ScriptedPlayer platform, {
    Size? size,
    FakeViewPadding? padding,
    ThemeData? theme,
  }) async {
    SharedPreferences.setMockInitialValues({});
    if (size != null) {
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
    }
    if (padding != null) {
      tester.view.padding = padding;
    }
    final store = LocalStore(await SharedPreferences.getInstance());
    final detail = await repository.detail(FixtureRepository.free);
    await tester.pumpWidget(
      MaterialApp(
        theme: theme ?? ThemeData.dark(),
        home: PlayerScreen(
          detail: detail,
          initialIndex: 0,
          initialPosition: 7,
          repository: repository,
          store: store,
          playerFactory: () => Player(platformPlayer: platform),
          videoBuilder: (controls) => controls,
        ),
      ),
    );
    await settleOperations(tester);
  }

  Future<void> unmount(WidgetTester tester, ScriptedPlayer player) async {
    await tester.pumpWidget(const SizedBox.shrink());
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
    tester.view.resetPadding();
    await settleOperations(tester);
    expect(player.disposed, isTrue);
    expect(tester.takeException(), isNull);
  }

  testWidgets(
    'duplicate errors switch once while keeping progress, rate and pause state',
    (tester) async {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repository, player);
      await tester.tap(find.byKey(const ValueKey('player-speed')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('1.5x').last);
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('关闭菜单'));
      await tester.pumpAndSettle();
      await player.seek(const Duration(seconds: 28));
      await tester.pump();
      await tester.tap(find.byTooltip('暂停播放'));
      await settleOperations(tester);
      player.fail();
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      await settleOperations(tester);
      expect(repository.fallbackCalls, 1);
      expect(repository.primaryCalls, 1);
      expect(player.opened.last.start, const Duration(seconds: 28));
      expect(player.state.rate, 1.5);
      expect(player.played.last, isFalse);
      expect(repository.active.length, 1);
      expect(find.text('暂时无法播放'), findsNothing);
      await unmount(tester, player);
      expect(repository.active, isEmpty);
    },
  );

  testWidgets(
    'recovery exhaustion releases sessions and manual retry keeps the saved position',
    (tester) async {
      final repository = RouteRepository()..broken = true;
      final player = ScriptedPlayer();
      await mount(tester, repository, player);
      for (var i = 0; i < 5; i++) {
        await tester.pump(const Duration(seconds: 1));
        await settleOperations(tester);
      }
      expect(repository.primaryCalls, 2);
      expect(repository.fallbackCalls, 2);
      expect(find.text('暂时无法播放'), findsOneWidget);
      expect(repository.active, isEmpty);
      await tester.pump(const Duration(seconds: 25));
      expect(repository.primaryCalls + repository.fallbackCalls, 4);
      repository.broken = false;
      await tester.tap(find.text('重试播放'));
      await settleOperations(tester);
      expect(player.opened.last.start, const Duration(seconds: 7));
      expect(find.text('暂时无法播放'), findsNothing);
      await unmount(tester, player);
      expect(repository.active, isEmpty);
    },
  );

  testWidgets(
    'switching episodes ignores a delayed fallback and frees both old plans',
    (tester) async {
      final repository = RouteRepository()..deferFallback = true;
      final player = ScriptedPlayer();
      await mount(tester, repository, player);
      player.fail();
      await tester.pump();
      await tester.pump(const Duration(seconds: 1));
      await settleOperations(tester);
      expect(repository.pending, isNotNull);
      await tester.tap(find.byKey(const ValueKey('play-episode-2')));
      await settleOperations(tester);
      final currentURL = player.opened.last.uri;
      final late = PlaybackPlan(
        url: 'https://media.test/late.mp4',
        session: 'late',
      );
      repository.active.add(late.session);
      repository.pending!.complete(late);
      await settleOperations(tester);
      expect(player.opened.last.uri, currentURL);
      expect(repository.active.length, 1);
      expect(repository.active, isNot(contains('late')));
      await unmount(tester, player);
      expect(repository.active, isEmpty);
    },
  );

  testWidgets('picture-in-picture hides app overlay controls', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(AppDevice.channel, (call) async {
      switch (call.method) {
        case 'pictureInPictureStatus':
          return {'supported': true, 'active': false};
        case 'enterPictureInPicture':
          return {'supported': true, 'active': true, 'requested': true};
      }
      return null;
    });
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repository, player, size: const Size(390, 844));
      expect(
        find.byKey(const ValueKey('player-picture-in-picture')),
        findsOneWidget,
      );
      expect(find.text('选集'), findsOneWidget);
      expect(find.text('简介'), findsOneWidget);
      expect(find.text('下载'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('player-picture-in-picture')));
      await tester.pump();
      await tester.pump();
      await settleOperations(tester);
      expect(
        find.byKey(const ValueKey('player-picture-in-picture')),
        findsNothing,
      );
      expect(find.byKey(const ValueKey('player-speed')), findsNothing);
      expect(find.byKey(const ValueKey('player-quality')), findsNothing);
      expect(find.byKey(const ValueKey('player-progress')), findsNothing);
      expect(find.text('选集'), findsNothing);
      expect(find.text('简介'), findsNothing);
      expect(find.text('下载'), findsNothing);
      await unmount(tester, player);
    } finally {
      messenger.setMockMethodCallHandler(AppDevice.channel, null);
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('mobile taps toggle controls and double taps toggle playback', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(
        tester,
        repository,
        player,
        size: const Size(390, 844),
        padding: const FakeViewPadding(top: 32),
      );
      final surface = tester.getRect(
        find.byKey(const ValueKey('player-gesture-surface')),
      );
      expect(surface.top, greaterThanOrEqualTo(32));
      final tapPoint = Offset(surface.center.dx, surface.top + 12);
      expect(find.byTooltip('暂停播放'), findsOneWidget);
      await tester.tapAt(tapPoint);
      await tester.pump(kDoubleTapTimeout + const Duration(milliseconds: 50));
      expect(player.state.playing, isTrue);
      await tester.tapAt(tapPoint);
      await tester.pump(kDoubleTapTimeout + const Duration(milliseconds: 50));
      expect(player.state.playing, isTrue);

      await tester.tapAt(tapPoint);
      await tester.pump(kDoubleTapMinTime);
      await tester.tapAt(tapPoint);
      await tester.pump();
      expect(player.state.playing, isFalse);

      await tester.pump(kDoubleTapTimeout);
      await tester.tapAt(tapPoint);
      await tester.pump(kDoubleTapMinTime);
      await tester.tapAt(tapPoint);
      await tester.pump();
      expect(player.state.playing, isTrue);
      await unmount(tester, player);
    } finally {
      debugDefaultTargetPlatformOverride = null;
      tester.view.resetPadding();
    }
  });

  testWidgets('compact player tools stay clustered instead of evenly spread', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(AppDevice.channel, (call) async {
      if (call.method == 'pictureInPictureStatus') {
        return {'supported': true, 'active': false};
      }
      return null;
    });
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repository, player, size: const Size(390, 844));
      final speed = tester.getRect(find.byKey(const ValueKey('player-speed')));
      final quality = tester.getRect(
        find.byKey(const ValueKey('player-quality')),
      );
      final route = tester.getRect(find.byKey(const ValueKey('player-route')));
      final pip = tester.getRect(
        find.byKey(const ValueKey('player-picture-in-picture')),
      );
      expect(quality.left - speed.right, lessThan(8));
      expect(route.left - quality.right, lessThan(8));
      expect(pip.left - route.right, lessThan(8));
      expect(pip.right, greaterThan(330));
      await unmount(tester, player);
    } finally {
      messenger.setMockMethodCallHandler(AppDevice.channel, null);
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('holding fast forward hides feedback and controls', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(
        tester,
        repository,
        player,
        size: const Size(390, 844),
        theme: ThemeData.light(),
      );
      final surface = tester.getRect(
        find.byKey(const ValueKey('player-gesture-surface')),
      );
      final target = Offset(surface.right - 20, surface.center.dy);
      final gesture = await tester.startGesture(target);
      await tester.pump(const Duration(milliseconds: 450));
      await tester.pump(const Duration(milliseconds: 200));
      expect(player.rates.last, 2);
      expect(find.textContaining('松开恢复'), findsNothing);
      expect(
        tester
            .widget<AnimatedOpacity>(
              find.byKey(const ValueKey('player-boost-badge')),
            )
            .opacity,
        1,
      );
      expect(
        find.byKey(const ValueKey('player-speed')).hitTestable(),
        findsNothing,
      );
      expect(
        find.byWidgetPredicate(
          (widget) =>
              widget is Container &&
              widget.decoration is BoxDecoration &&
              (widget.decoration as BoxDecoration).color == Colors.black87,
        ),
        findsNothing,
      );
      await gesture.up();
      await tester.pump();
      expect(player.rates.last, 1);
      expect(find.textContaining('恢复'), findsNothing);
      expect(
        tester
            .widget<AnimatedOpacity>(
              find.byKey(const ValueKey('player-boost-badge')),
            )
            .opacity,
        0,
      );
      await unmount(tester, player);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets(
    'desktop hover and seek keep controls hidden while taps and space toggle them',
    (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.windows;
      try {
        final repository = RouteRepository();
        final player = ScriptedPlayer();
        await mount(tester, repository, player, size: const Size(1280, 720));
        final controls = find.byKey(const ValueKey('player-speed'));
        expect(controls.hitTestable(), findsOneWidget);
        final surface = tester.getRect(
          find.byKey(const ValueKey('player-gesture-surface')),
        );
        final tapPoint = Offset(surface.center.dx, surface.top + 12);
        await tester.pump(const Duration(seconds: 5));
        expect(controls.hitTestable(), findsNothing);

        final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
        await mouse.addPointer(location: tapPoint);
        await mouse.moveTo(Offset(surface.center.dx, surface.center.dy));
        await tester.pump();
        expect(controls.hitTestable(), findsNothing);

        await mouse.moveTo(Offset(surface.center.dx, surface.bottom - 8));
        await tester.pump();
        expect(controls.hitTestable(), findsOneWidget);

        await tester.tapAt(tapPoint);
        await tester.pump();
        expect(controls.hitTestable(), findsNothing);
        await tester.tapAt(tapPoint);
        await tester.pump();
        expect(controls.hitTestable(), findsOneWidget);

        await tester.sendKeyDownEvent(LogicalKeyboardKey.space);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.space);
        await tester.pump();
        expect(player.state.playing, isFalse);
        expect(controls.hitTestable(), findsOneWidget);

        await tester.sendKeyDownEvent(LogicalKeyboardKey.space);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.space);
        await tester.pump();
        expect(player.state.playing, isTrue);
        expect(controls.hitTestable(), findsNothing);

        await tester.sendKeyDownEvent(LogicalKeyboardKey.arrowLeft);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.arrowLeft);
        await tester.pump();
        expect(find.textContaining('后退至'), findsNothing);
        expect(controls.hitTestable(), findsNothing);

        await mouse.removePointer();
        await unmount(tester, player);
      } finally {
        debugDefaultTargetPlatformOverride = null;
      }
    },
  );

  testWidgets('manual route selection is reused by the next episode', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.windows;
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repository, player, size: const Size(1280, 720));
      expect(find.byKey(const ValueKey('player-route')), findsOneWidget);
      expect(find.text('线路 1'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('player-route')));
      await tester.pumpAndSettle();
      expect(find.text('播放线路'), findsWidgets);
      await tester.tap(find.byKey(const ValueKey('menu-route-1')));
      await tester.pumpAndSettle();
      expect(repository.routeCalls, 1);
      expect(repository.requestedRoutes.last, 1);
      expect(find.text('线路 2'), findsOneWidget);
      expect(player.state.playing, isTrue);
      await tester.tap(find.byTooltip('下一集'));
      await settleOperations(tester);
      expect(repository.requestedEpisodes.last, 2);
      expect(repository.requestedRoutes.last, 1);
      expect(find.text('线路 2'), findsOneWidget);
      await unmount(tester, player);
      expect(repository.active, isEmpty);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('switching episodes keeps the control overlay hidden', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.windows;
    try {
      final repository = RouteRepository();
      final player = ScriptedPlayer();
      await mount(tester, repository, player, size: const Size(1280, 720));
      final controls = find.byKey(const ValueKey('player-speed'));
      expect(controls.hitTestable(), findsOneWidget);
      await tester.tap(find.byTooltip('下一集'));
      await settleOperations(tester);
      expect(repository.requestedEpisodes.last, 2);
      expect(player.state.playing, isTrue);
      expect(controls.hitTestable(), findsNothing);
      await unmount(tester, player);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });
}
