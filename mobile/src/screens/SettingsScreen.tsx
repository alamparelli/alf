import React, { useState, useEffect } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  SafeAreaView,
  Alert,
  ScrollView,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { getServerUrl, clearAuth } from '../services/auth';
import { colors, spacing, radius, typography } from '../theme';

interface Props {
  onDisconnect: () => void;
}

export default function SettingsScreen({ onDisconnect }: Props) {
  const [serverUrl, setServerUrl] = useState('');

  useEffect(() => {
    getServerUrl().then((url) => setServerUrl(url || ''));
  }, []);

  const handleDisconnect = () => {
    Alert.alert(
      'Disconnect',
      'This will clear your credentials. You\'ll need to reconfigure.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Disconnect',
          style: 'destructive',
          onPress: async () => {
            await clearAuth();
            onDisconnect();
          },
        },
      ],
    );
  };

  return (
    <SafeAreaView style={styles.container}>
      <ScrollView contentContainerStyle={styles.scroll}>
        {/* Connection Section */}
        <View style={styles.section}>
          <Text style={styles.sectionHeader}>Connection</Text>
          <View style={styles.card}>
            <View style={styles.row}>
              <View style={styles.rowIcon}>
                <Ionicons name="server-outline" size={18} color={colors.accent} />
              </View>
              <View style={styles.rowContent}>
                <Text style={styles.rowLabel}>Server</Text>
                <Text style={styles.rowValue} numberOfLines={1}>
                  {serverUrl || 'Not configured'}
                </Text>
              </View>
              <View style={styles.statusDot} />
            </View>
          </View>
        </View>

        {/* Account Section */}
        <View style={styles.section}>
          <Text style={styles.sectionHeader}>Account</Text>
          <View style={styles.card}>
            <TouchableOpacity
              style={styles.row}
              onPress={handleDisconnect}
              activeOpacity={0.6}
            >
              <View style={[styles.rowIcon, styles.rowIconDestructive]}>
                <Ionicons name="log-out-outline" size={18} color={colors.destructive} />
              </View>
              <View style={styles.rowContent}>
                <Text style={styles.rowLabelDestructive}>Disconnect</Text>
                <Text style={styles.rowValue}>Clear credentials and reconfigure</Text>
              </View>
              <Ionicons name="chevron-forward" size={16} color={colors.textTertiary} />
            </TouchableOpacity>
          </View>
        </View>

        <Text style={styles.version}>ALF Mobile v0.1.0</Text>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  scroll: {
    paddingTop: spacing.lg,
    paddingBottom: spacing.xxxl,
  },
  section: {
    marginBottom: spacing.xl,
  },
  sectionHeader: {
    ...typography.footnote,
    color: colors.textSecondary,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
    paddingHorizontal: spacing.xl,
    marginBottom: spacing.sm,
  },
  card: {
    backgroundColor: colors.surface,
    marginHorizontal: spacing.lg,
    borderRadius: radius.md,
    overflow: 'hidden',
  },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingHorizontal: spacing.lg,
    paddingVertical: spacing.md,
    gap: spacing.md,
  },
  rowIcon: {
    width: 32,
    height: 32,
    borderRadius: radius.sm,
    backgroundColor: colors.accentSoft,
    justifyContent: 'center',
    alignItems: 'center',
  },
  rowIconDestructive: {
    backgroundColor: colors.destructiveSoft,
  },
  rowContent: {
    flex: 1,
    gap: 1,
  },
  rowLabel: {
    ...typography.callout,
    color: colors.textPrimary,
  },
  rowLabelDestructive: {
    ...typography.callout,
    color: colors.destructive,
  },
  rowValue: {
    ...typography.footnote,
    color: colors.textTertiary,
  },
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: colors.success,
  },
  version: {
    ...typography.caption,
    textAlign: 'center',
    marginTop: spacing.xxl,
  },
});
