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
} from 'react-native';
import { setServerUrl, setToken } from '../services/auth';
import { healthCheck } from '../services/api';

interface Props {
  onComplete: () => void;
}

export default function SetupScreen({ onComplete }: Props) {
  const [url, setUrl] = useState('');
  const [token, setTokenValue] = useState('');
  const [loading, setLoading] = useState(false);

  const handleConnect = async () => {
    if (!url.trim() || !token.trim()) {
      Alert.alert('Error', 'Both fields are required');
      return;
    }

    setLoading(true);
    try {
      await setServerUrl(url.trim());
      await setToken(token.trim());

      const ok = await healthCheck();
      if (!ok) {
        Alert.alert('Connection Failed', 'Could not reach the server. Check URL and token.');
        setLoading(false);
        return;
      }

      onComplete();
    } catch (e: any) {
      Alert.alert('Error', e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <SafeAreaView style={styles.container}>
      <KeyboardAvoidingView
        behavior={Platform.OS === 'ios' ? 'padding' : 'height'}
        style={styles.inner}
      >
        <Text style={styles.title}>ALF</Text>
        <Text style={styles.subtitle}>Connect to your instance</Text>

        <TextInput
          style={styles.input}
          placeholder="Server URL (e.g. http://192.168.1.100:8080)"
          placeholderTextColor="#666"
          value={url}
          onChangeText={setUrl}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
        />

        <TextInput
          style={styles.input}
          placeholder="Auth Token"
          placeholderTextColor="#666"
          value={token}
          onChangeText={setTokenValue}
          autoCapitalize="none"
          autoCorrect={false}
          secureTextEntry
        />

        <TouchableOpacity
          style={[styles.button, loading && styles.buttonDisabled]}
          onPress={handleConnect}
          disabled={loading}
        >
          <Text style={styles.buttonText}>
            {loading ? 'Connecting...' : 'Connect'}
          </Text>
        </TouchableOpacity>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a2e',
  },
  inner: {
    flex: 1,
    justifyContent: 'center',
    paddingHorizontal: 32,
  },
  title: {
    fontSize: 48,
    fontWeight: '700',
    color: '#e0e0e0',
    textAlign: 'center',
    marginBottom: 8,
  },
  subtitle: {
    fontSize: 16,
    color: '#888',
    textAlign: 'center',
    marginBottom: 40,
  },
  input: {
    backgroundColor: '#2a2a4e',
    borderRadius: 12,
    padding: 16,
    fontSize: 16,
    color: '#e0e0e0',
    marginBottom: 16,
  },
  button: {
    backgroundColor: '#6c63ff',
    borderRadius: 12,
    padding: 16,
    alignItems: 'center',
    marginTop: 8,
  },
  buttonDisabled: {
    opacity: 0.6,
  },
  buttonText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
});
