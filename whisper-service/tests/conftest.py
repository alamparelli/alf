"""Shared fixtures for whisper-service tests."""

import os
import sys
import pytest

# Add parent directory to path so tests can import modules
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

os.environ.setdefault("WHISPER_SHARED_SECRET", "test-secret")
