import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  Modal,
  StyleSheet,
  Pressable,
} from 'react-native';

const REACTIONS = ['👍', '👎', '🔥', '❤️', '😂', '🤔', '👏', '💯', '😢', '🎉'];

interface Props {
  onSelect: (emoji: string) => void;
  onClose: () => void;
}

export default function ReactionPicker({ onSelect, onClose }: Props) {
  return (
    <Modal transparent animationType="fade" onRequestClose={onClose}>
      <Pressable style={styles.overlay} onPress={onClose}>
        <View style={styles.picker}>
          {REACTIONS.map((emoji) => (
            <TouchableOpacity
              key={emoji}
              style={styles.emojiBtn}
              onPress={() => onSelect(emoji)}
            >
              <Text style={styles.emoji}>{emoji}</Text>
            </TouchableOpacity>
          ))}
        </View>
      </Pressable>
    </Modal>
  );
}

const styles = StyleSheet.create({
  overlay: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    backgroundColor: 'rgba(0,0,0,0.5)',
  },
  picker: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    justifyContent: 'center',
    backgroundColor: '#2a2a4e',
    borderRadius: 16,
    padding: 12,
    maxWidth: 300,
    gap: 8,
  },
  emojiBtn: {
    width: 48,
    height: 48,
    justifyContent: 'center',
    alignItems: 'center',
    borderRadius: 24,
    backgroundColor: 'rgba(255,255,255,0.05)',
  },
  emoji: {
    fontSize: 28,
  },
});
