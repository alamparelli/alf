import React, { useState } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  SafeAreaView,
  Alert,
  KeyboardAvoidingView,
  Platform,
  ActivityIndicator,
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { setServerUrl, setToken } from '../services/auth';
import { healthCheck } from '../services/api';
import { colors, spacing, radius, typography } from '../theme';

interface Props {
  onComplete: () => void;
}

export default function SetupScreen({ onComplete }: Props) {
  const [url, setUrl] = useState('');
  const [token, setTokenValue] = useState('');
  const [loading, setLoading] = useState(false);
  const [step, setStep] = useState<'url' | 'token'>('url');

  const handleConnect = async () => {
    if (!url.trim() || !token.trim()) {
      Alert.alert('Missing fields', 'Both server URL and token are required.');
      return;
    }

    setLoading(true);
    try {
      await setServerUrl(url.trim());
      await setToken(token.trim());
      await healthCheck();
      onComplete();
    } catch (e: any) {
      const msg = e.message?.includes('Network request failed')
        ? 'Could not reach the server. Check the URL and port.'
        : e.message || 'Unknown error';
      Alert.alert('Connection failed', msg);
    } finally {
      setLoading(false);
    }
  };

  const canProceedToToken = url.trim().length > 0;
  const canConnect = url.trim().length > 0 && token.trim().length > 0;

  return (
    <SafeAreaView style={styles.container}>
      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
        style={styles.inner}
      >
        <View style={styles.header}>
          <View style={styles.iconContainer}>
            <Ionicons name="planet" size={40} color={colors.accent} />
          </View>
          <Text style={styles.title}>ALF</Text>
          <Text style={styles.subtitle}>Connect to your instance</Text>
        </View>

        <View style={styles.form}>
          <View style={styles.inputGroup}>
            <Text style={styles.label}>Server</Text>
            <View style={styles.inputWrapper}>
              <Ionicons
                name="globe-outline"
                size={18}
                color={colors.textTertiary}
                style={styles.inputIcon}
              />
              <TextInput
                style={styles.input}
                placeholder="http://192.168.1.100:8080"
                placeholderTextColor={colors.textTertiary}
                value={url}
                onChangeText={setUrl}
                autoCapitalize="none"
                autoCorrect={false}
                keyboardType="url"
                returnKeyType="next"
                onSubmitEditing={() => canProceedToToken && setStep('token')}
              />
            </View>
          </View>

          <View style={styles.inputGroup}>
            <Text style={styles.label}>Token</Text>
            <View style={styles.inputWrapper}>
              <Ionicons
                name="key-outline"
                size={18}
                color={colors.textTertiary}
                style={styles.inputIcon}
              />
              <TextInput
                style={styles.input}
                placeholder="Authentication token"
                placeholderTextColor={colors.textTertiary}
                value={token}
                onChangeText={setTokenValue}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
                returnKeyType="go"
                onSubmitEditing={handleConnect}
              />
            </View>
          </View>

          <TouchableOpacity
            style={[styles.button, (!canConnect || loading) && styles.buttonDisabled]}
            onPress={handleConnect}
            disabled={!canConnect || loading}
            activeOpacity={0.7}
          >
            {loading ? (
              <ActivityIndicator color={colors.textPrimary} size="small" />
            ) : (
              <Text style={styles.buttonText}>Connect</Text>
            )}
          </TouchableOpacity>
        </View>

        <Text style={styles.footnote}>
          Your credentials are stored securely on-device.
        </Text>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  inner: {
    flex: 1,
    justifyContent: 'center',
    paddingHorizontal: spacing.xxl,
  },
  header: {
    alignItems: 'center',
    marginBottom: spacing.xxxl,
  },
  iconContainer: {
    width: 72,
    height: 72,
    borderRadius: radius.lg,
    backgroundColor: colors.accentSoft,
    justifyContent: 'center',
    alignItems: 'center',
    marginBottom: spacing.lg,
  },
  title: {
    ...typography.largeTitle,
    marginBottom: spacing.xs,
  },
  subtitle: {
    ...typography.subhead,
  },
  form: {
    gap: spacing.lg,
  },
  inputGroup: {
    gap: spacing.sm,
  },
  label: {
    ...typography.footnote,
    color: colors.textSecondary,
    textTransform: 'uppercase',
    letterSpacing: 0.5,
    paddingLeft: spacing.xs,
  },
  inputWrapper: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: colors.surface,
    borderRadius: radius.md,
    borderWidth: 0.5,
    borderColor: colors.separator,
  },
  inputIcon: {
    paddingLeft: spacing.md,
  },
  input: {
    flex: 1,
    paddingHorizontal: spacing.md,
    paddingVertical: 14,
    ...typography.body,
    color: colors.textPrimary,
  },
  button: {
    backgroundColor: colors.accent,
    borderRadius: radius.md,
    paddingVertical: 15,
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: spacing.sm,
    height: 52,
  },
  buttonDisabled: {
    opacity: 0.4,
  },
  buttonText: {
    color: colors.textPrimary,
    fontSize: 17,
    fontWeight: '600',
    letterSpacing: -0.41,
  },
  footnote: {
    ...typography.caption,
    textAlign: 'center',
    marginTop: spacing.xxl,
  },
});
