import 'package:flutter/gestures.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';

import 'package:duanju_app/player_interactions.dart';

import 'player_fixtures.dart';

void main() {
  test('touching the right side temporarily boosts playback', () async {
    final platform = ScriptedPlayer();
    final player = Player(platformPlayer: platform);
    await platform.open(Media('https://media.test/working.mp4'));
    final interactions = PlayerInteractions(
      player: player,
      available: () => true,
      baseSpeed: () => 1.5,
      onTogglePlayback: () {},
      onFullscreen: () {},
      onEpisode: (_) => '',
    );

    interactions.pointerDown(
      PointerDownEvent(
        pointer: 1,
        position: const Offset(90, 50),
        buttons: kPrimaryButton,
        kind: PointerDeviceKind.touch,
        timeStamp: Duration.zero,
      ),
      swipeEnabled: true,
      height: 100,
      width: 100,
    );
    await Future<void>.delayed(const Duration(milliseconds: 400));
    expect(platform.rates, contains(2));
    expect(interactions.feedback, isEmpty);

    interactions.pointerUp(
      PointerUpEvent(
        pointer: 1,
        position: const Offset(90, 50),
        buttons: 0,
        kind: PointerDeviceKind.touch,
        timeStamp: const Duration(milliseconds: 400),
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(platform.rates.last, 1.5);
    expect(interactions.feedback, isEmpty);

    interactions.dispose();
    await player.dispose();
  });

  test('center touch keeps the existing temporary boost behavior', () async {
    final platform = ScriptedPlayer();
    final player = Player(platformPlayer: platform);
    await platform.open(Media('https://media.test/working.mp4'));
    final interactions = PlayerInteractions(
      player: player,
      available: () => true,
      baseSpeed: () => 1,
      onTogglePlayback: () {},
      onFullscreen: () {},
      onEpisode: (_) => '',
    );

    interactions.pointerDown(
      PointerDownEvent(
        pointer: 1,
        position: const Offset(50, 50),
        buttons: kPrimaryButton,
        kind: PointerDeviceKind.touch,
        timeStamp: Duration.zero,
      ),
      swipeEnabled: true,
      height: 100,
      width: 100,
    );
    await Future<void>.delayed(const Duration(milliseconds: 400));
    expect(platform.rates, contains(2));
    expect(interactions.feedback, isEmpty);

    interactions.pointerCancel(
      PointerCancelEvent(
        pointer: 1,
        position: const Offset(50, 50),
        buttons: 0,
        kind: PointerDeviceKind.touch,
        timeStamp: const Duration(milliseconds: 400),
      ),
    );
    await Future<void>.delayed(Duration.zero);

    interactions.dispose();
    await player.dispose();
  });

  test(
    'seek shows no overlay and reveals no controls while volume hints do',
    () async {
      final platform = ScriptedPlayer();
      final player = Player(platformPlayer: platform);
      await platform.open(Media('https://media.test/working.mp4'));
      final interactions = PlayerInteractions(
        player: player,
        available: () => true,
        baseSpeed: () => 1,
        onTogglePlayback: () {},
        onFullscreen: () {},
        onEpisode: (_) => '',
      );

      interactions.seek(5);
      await Future<void>.delayed(Duration.zero);
      expect(interactions.feedback, isEmpty);
      expect(interactions.feedbackRevealsControls, isFalse);

      interactions.seek(-5);
      await Future<void>.delayed(Duration.zero);
      expect(interactions.feedback, isEmpty);
      expect(interactions.feedbackRevealsControls, isFalse);

      interactions.changeVolume(5);
      expect(interactions.feedbackRevealsControls, isTrue);

      interactions.dispose();
      await player.dispose();
    },
  );
}
