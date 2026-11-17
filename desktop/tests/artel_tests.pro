QT += core gui qml quick quickcontrols2 testlib
CONFIG += c++17 testcase warn_on
QMAKE_CXXFLAGS += -Wextra -Werror
TARGET = artel_tests
TEMPLATE = app
SOURCES += artel_tests.cpp
RESOURCES += ../src/resources.qrc
